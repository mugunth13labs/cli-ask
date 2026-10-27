//go:build windows

package main

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"unsafe"
)

type WindowsPTY struct {
	hPC        uintptr
	inRead     syscall.Handle
	inWrite    syscall.Handle
	outRead    syscall.Handle
	outWrite   syscall.Handle
	procHandle syscall.Handle
	threadHnd  syscall.Handle
	isConPTY   bool

	// Fallback pipe fields
	stdinPipe  io.WriteCloser
	stdoutPipe io.ReadCloser
	cmd        *exec.Cmd
}

	// coord is defined in term_windows.go

type startupInfoEx struct {
	syscall.StartupInfo
	lpAttributeList uintptr
}

const (
	procThreadAttributePseudoConsole = 0x00020016
	extendedStartupInfoPresent       = 0x00080000
	createNewConsole                 = 0x00000010
)

var (
	modKernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole          = modKernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole          = modKernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole           = modKernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttrList = modKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute    = modKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttrList     = modKernel32.NewProc("DeleteProcThreadAttributeList")
)

func StartPTY(rows, cols int) (PTYSession, error) {
	return startPipeShell(rows, cols)
}

func packCoord(cols, rows int) uintptr {
	return uintptr(uint32(uint16(cols)) | (uint32(uint16(rows)) << 16))
}

func startConPTY(rows, cols int) (*WindowsPTY, error) {
	var inRead, inWrite, outRead, outWrite syscall.Handle

	sa := syscall.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(syscall.SecurityAttributes{})),
		InheritHandle: 1,
	}

	if err := syscall.CreatePipe(&inRead, &inWrite, &sa, 0); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	if err := syscall.CreatePipe(&outRead, &outWrite, &sa, 0); err != nil {
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}

	var hPC uintptr
	r1, _, err := procCreatePseudoConsole.Call(
		packCoord(cols, rows),
		uintptr(inRead),
		uintptr(outWrite),
		0,
		uintptr(unsafe.Pointer(&hPC)),
	)
	if r1 != 0 {
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreatePseudoConsole failed: %v", err)
	}

	// Calculate attribute list size
	var attrListSize uintptr
	procInitializeProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrListSize)))
	if attrListSize == 0 {
		procClosePseudoConsole.Call(hPC)
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("InitializeProcThreadAttributeList failed to calculate size")
	}

	attrList := make([]byte, attrListSize)
	r1, _, err = procInitializeProcThreadAttrList.Call(
		uintptr(unsafe.Pointer(&attrList[0])),
		1,
		0,
		uintptr(unsafe.Pointer(&attrListSize)),
	)
	if r1 == 0 {
		procClosePseudoConsole.Call(hPC)
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("InitializeProcThreadAttributeList failed: %v", err)
	}

	r1, _, err = procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(&attrList[0])),
		0,
		procThreadAttributePseudoConsole,
		hPC,
		unsafe.Sizeof(hPC),
		0,
		0,
	)
	if r1 == 0 {
		procDeleteProcThreadAttrList.Call(uintptr(unsafe.Pointer(&attrList[0])))
		procClosePseudoConsole.Call(hPC)
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("UpdateProcThreadAttribute failed: %v", err)
	}

	var siEx startupInfoEx
	siEx.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.lpAttributeList = uintptr(unsafe.Pointer(&attrList[0]))

	var pi syscall.ProcessInformation

	// Find powershell or cmd
	shellPath, err := exec.LookPath("powershell.exe")
	var cmdLine string
	if err == nil {
		cmdLine = fmt.Sprintf(`"%s" -NoLogo -NoExit`, shellPath)
	} else {
		cmdPath, _ := exec.LookPath("cmd.exe")
		cmdLine = fmt.Sprintf(`"%s" /k`, cmdPath)
	}

	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)

	err = syscall.CreateProcess(
		nil,
		cmdLinePtr,
		nil,
		nil,
		false,
		extendedStartupInfoPresent|createNewConsole,
		nil,
		nil,
		(*syscall.StartupInfo)(unsafe.Pointer(&siEx)),
		&pi,
	)


	procDeleteProcThreadAttrList.Call(uintptr(unsafe.Pointer(&attrList[0])))

	if err != nil {
		procClosePseudoConsole.Call(hPC)
		syscall.CloseHandle(inRead)
		syscall.CloseHandle(inWrite)
		syscall.CloseHandle(outRead)
		syscall.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreateProcess failed: %w", err)
	}

	return &WindowsPTY{
		hPC:        hPC,
		inRead:     inRead,
		inWrite:    inWrite,
		outRead:    outRead,
		outWrite:   outWrite,
		procHandle: pi.Process,
		threadHnd:  pi.Thread,
		isConPTY:   true,
	}, nil
}

func startPipeShell(rows, cols int) (*WindowsPTY, error) {
	shellPath, err := exec.LookPath("powershell.exe")
	if err != nil {
		shellPath, _ = exec.LookPath("cmd.exe")
	}

	cmd := exec.Command(shellPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &WindowsPTY{
		stdinPipe:  stdin,
		stdoutPipe: stdout,
		cmd:        cmd,
		isConPTY:   false,
	}, nil
}

func (p *WindowsPTY) Read(b []byte) (int, error) {
	if p.isConPTY {
		var bytesRead uint32
		err := syscall.ReadFile(p.outRead, b, &bytesRead, nil)
		if err != nil {
			return int(bytesRead), err
		}
		return int(bytesRead), nil
	}
	return p.stdoutPipe.Read(b)
}

func (p *WindowsPTY) Write(b []byte) (int, error) {
	if p.isConPTY {
		var bytesWritten uint32
		err := syscall.WriteFile(p.inWrite, b, &bytesWritten, nil)
		if err != nil {
			return int(bytesWritten), err
		}
		return int(bytesWritten), nil
	}
	return p.stdinPipe.Write(b)
}

func (p *WindowsPTY) Resize(rows, cols int) error {
	if p.isConPTY && p.hPC != 0 {
		r1, _, err := procResizePseudoConsole.Call(p.hPC, packCoord(cols, rows))
		if r1 != 0 {
			return err
		}
	}
	return nil
}

func (p *WindowsPTY) Close() error {
	_ = p.Kill()
	if p.isConPTY {
		if p.hPC != 0 {
			procClosePseudoConsole.Call(p.hPC)
			p.hPC = 0
		}
		if p.inRead != 0 {
			syscall.CloseHandle(p.inRead)
			p.inRead = 0
		}
		if p.outWrite != 0 {
			syscall.CloseHandle(p.outWrite)
			p.outWrite = 0
		}
		if p.inWrite != 0 {
			syscall.CloseHandle(p.inWrite)
			p.inWrite = 0
		}
		if p.outRead != 0 {
			syscall.CloseHandle(p.outRead)
			p.outRead = 0
		}
		if p.procHandle != 0 {
			syscall.CloseHandle(p.procHandle)
			p.procHandle = 0
		}
		if p.threadHnd != 0 {
			syscall.CloseHandle(p.threadHnd)
			p.threadHnd = 0
		}
		return nil
	}

	if p.stdinPipe != nil {
		p.stdinPipe.Close()
	}
	if p.stdoutPipe != nil {
		p.stdoutPipe.Close()
	}
	return nil
}

func (p *WindowsPTY) Wait() error {
	if p.isConPTY {
		if p.procHandle != 0 {
			_, _ = syscall.WaitForSingleObject(p.procHandle, syscall.INFINITE)
		}
		return nil
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}

func (p *WindowsPTY) Kill() error {
	if p.isConPTY {
		if p.procHandle != 0 {
			_ = syscall.TerminateProcess(p.procHandle, 1)
		}
		return nil
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}
