package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(from, to string) error {
	f, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// REPLACE_EXISTING | WRITE_THROUGH, on the same volume as the temp file.
	ok, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), 0x1|0x8)
	if ok == 0 {
		return callErr
	}
	return nil
}

func lockDataDir(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// The kernel releases this exclusive handle on crashes. A leftover file is not a stale lock.
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, errors.New("data directory is locked by another engine or cannot be opened")
	}
	f := os.NewFile(uintptr(h), path)
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func killTree(p *os.Process) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(p.Pid), "/T", "/F")
	configureProcess(cmd)
	if err := cmd.Run(); err != nil {
		return p.Kill()
	}
	return nil
}

var (
	createJobObject     = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateJobObjectW")
	setJobInformation   = syscall.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject")
	queryJobInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryInformationJobObject")
	assignProcessToJob  = syscall.NewLazyDLL("kernel32.dll").NewProc("AssignProcessToJobObject")
	terminateJobObject  = syscall.NewLazyDLL("kernel32.dll").NewProc("TerminateJobObject")
	resumeOwnedProcess  = syscall.NewLazyDLL("ntdll.dll").NewProc("NtResumeProcess")
)

type runtimeProcessGroup struct {
	mu             sync.Mutex
	job            syscall.Handle
	closed         bool
	drained        bool
	turnActive     bool
	infrastructure map[uintptr]bool
	workload       map[uintptr]bool
}

type ownedProcess struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	group     *runtimeProcessGroup
	process   syscall.Handle // Independent of the handle released by cmd.Wait.
	exclusive bool
	stopped   bool
	stopErr   error
}

func newRuntimeProcessGroup() (*runtimeProcessGroup, error) {
	job, _, err := createJobObject.Call(0, 0)
	if job == 0 {
		return nil, errors.New("Cannot create the owned harness job.")
	}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION, pointer-size aware on Windows.
	limits := struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
		IOCounters              [6]uint64
		ProcessMemoryLimit      uintptr
		JobMemoryLimit          uintptr
		PeakProcessMemoryUsed   uintptr
		PeakJobMemoryUsed       uintptr
	}{LimitFlags: 0x2000} // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, err := setJobInformation.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return nil, fmt.Errorf("Cannot configure the owned harness job: %w", err)
	}
	return &runtimeProcessGroup{job: syscall.Handle(job), infrastructure: map[uintptr]bool{}, workload: map[uintptr]bool{}}, nil
}

func prepareOwnedProcess(cmd *exec.Cmd) (*ownedProcess, error) {
	group, err := newRuntimeProcessGroup()
	if err != nil {
		return nil, err
	}
	return prepareRuntimeOwnedProcess(cmd, group, true), nil
}

func prepareSessionOwnedProcess(cmd *exec.Cmd, group *runtimeProcessGroup) (*ownedProcess, error) {
	return prepareRuntimeOwnedProcess(cmd, group, false), nil
}

func prepareRuntimeOwnedProcess(cmd *exec.Cmd, group *runtimeProcessGroup, exclusive bool) *ownedProcess {
	// Launch suspended: assigning a job after an ordinary Start leaves a
	// child-spawn race. No target code runs until assignment has succeeded.
	configureProcess(cmd)
	cmd.SysProcAttr.CreationFlags |= 0x4 // CREATE_SUSPENDED
	return &ownedProcess{cmd: cmd, group: group, exclusive: exclusive}
}

func (o *ownedProcess) Attach() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		_ = o.cmd.Process.Kill()
		return errors.New("Owned harness was cancelled before job assignment.")
	}
	o.group.mu.Lock()
	defer o.group.mu.Unlock()
	if o.group.closed || o.group.job == 0 {
		_ = o.cmd.Process.Kill()
		return errors.New("Owned harness runtime closed before job assignment.")
	}
	// This PID comes directly from the still-suspended process we just created.
	h, err := syscall.OpenProcess(0x0100|0x0800|syscall.PROCESS_TERMINATE|syscall.SYNCHRONIZE, false, uint32(o.cmd.Process.Pid))
	if err != nil {
		_ = o.cmd.Process.Kill()
		return errors.New("Cannot open the suspended owned harness.")
	}
	defer func() {
		if h != 0 {
			_ = syscall.CloseHandle(h)
		}
	}()
	if ok, _, _ := assignProcessToJob.Call(uintptr(o.group.job), uintptr(h)); ok == 0 {
		_ = o.cmd.Process.Kill()
		return errors.New("Cannot contain the harness in its owned job; execution was refused.")
	}
	if status, _, _ := resumeOwnedProcess.Call(uintptr(h)); status != 0 {
		_ = o.cmd.Process.Kill()
		return errors.New("Cannot resume the contained harness.")
	}
	if !o.exclusive {
		// Keep a stable process identity through concurrent cmd.Cancel/cmd.Wait.
		// ProcessState is not synchronized, and Go 1.24's Windows Wait releases
		// cmd.Process's handle, after which Kill returns EINVAL.
		o.process, h = h, 0
	}
	return nil
}

func stopOwnedWindowsProcess(process syscall.Handle) error {
	state, err := syscall.WaitForSingleObject(process, 0)
	switch state {
	case syscall.WAIT_OBJECT_0:
		return nil // Confirmed exit: do not issue a redundant termination.
	case syscall.WAIT_TIMEOUT:
		// The retained handle cannot be released by cmd.Wait or refer to a reused PID.
	default:
		if err != nil {
			return os.NewSyscallError("WaitForSingleObject", err)
		}
		return fmt.Errorf("Unexpected owned harness process wait result: %d", state)
	}
	err = syscall.TerminateProcess(process, 1)
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		// Job shutdown and cmd.Cancel can race: Windows refuses another
		// termination while exit is still in progress, before the handle is
		// signaled. Wait on this exact owned identity, with the same bounded
		// drainage budget, and accept only a confirmed exit.
		if state, _ := syscall.WaitForSingleObject(process, 5000); state == syscall.WAIT_OBJECT_0 {
			return nil
		}
	}
	return os.NewSyscallError("TerminateProcess", err)
}

func (o *ownedProcess) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return o.stopErr
	}
	o.stopped = true
	if o.exclusive {
		o.stopErr = o.group.Close()
	} else if o.process != 0 {
		o.stopErr = stopOwnedWindowsProcess(o.process)
		o.stopErr = errors.Join(o.stopErr, syscall.CloseHandle(o.process))
		o.process = 0
	} else if o.cmd.Process != nil {
		// Cancellation before Attach: the process is still suspended and no
		// caller has started Wait yet. Attach will refuse to resume it.
		o.stopErr = o.cmd.Process.Kill()
	}
	return o.stopErr
}

func (o *ownedProcess) Close() error { return o.Stop() }

func (o *ownedProcess) Drained() bool {
	if !o.exclusive {
		return false
	}
	o.group.mu.Lock()
	defer o.group.mu.Unlock()
	return o.group.drained
}

func (g *runtimeProcessGroup) BeginTurn() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("Owned harness runtime closed before the next turn.")
	}
	active, err := g.activeProcessesLocked()
	if err != nil {
		return err
	}
	g.updateWorkloadLocked(active)
	g.turnActive = true
	return nil
}

func (g *runtimeProcessGroup) EndTurn() {
	g.mu.Lock()
	g.turnActive = false
	g.mu.Unlock()
}

func (g *runtimeProcessGroup) activeProcessesLocked() (map[uintptr]bool, error) {
	const capacity = 4096
	buffer := make([]byte, 8+capacity*int(unsafe.Sizeof(uintptr(0))))
	var returned uint32
	ok, _, _ := queryJobInformation.Call(uintptr(g.job), 3, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&returned)))
	if ok == 0 {
		return nil, errors.New("Cannot inspect the session runtime process list.")
	}
	header := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
	if header[0] > capacity || header[1] > header[0] {
		return nil, errors.New("Session runtime process list exceeded its supported size.")
	}
	active := make(map[uintptr]bool, header[1])
	base := uintptr(unsafe.Pointer(&buffer[0])) + 8
	step := unsafe.Sizeof(uintptr(0))
	for i := uintptr(0); i < uintptr(header[1]); i++ {
		pid := *(*uintptr)(unsafe.Pointer(base + i*step))
		if pid != 0 {
			active[pid] = true
		}
	}
	return active, nil
}

func (g *runtimeProcessGroup) MarkInfrastructure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	active, err := g.activeProcessesLocked()
	if err != nil {
		return
	}
	for pid := range active {
		if !g.workload[pid] {
			g.infrastructure[pid] = true
		}
	}
}

func (g *runtimeProcessGroup) updateWorkloadLocked(active map[uintptr]bool) {
	for pid := range g.infrastructure {
		if !active[pid] {
			delete(g.infrastructure, pid)
		}
	}
	for pid := range g.workload {
		if !active[pid] {
			delete(g.workload, pid)
		}
	}
	for pid := range active {
		if !g.infrastructure[pid] {
			g.workload[pid] = true
		}
	}
}

func (g *runtimeProcessGroup) HasWorkload() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.turnActive {
		return false
	}
	active, err := g.activeProcessesLocked()
	if err != nil {
		return true
	}
	g.updateWorkloadLocked(active)
	return len(g.workload) > 0
}

func (g *runtimeProcessGroup) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	defer func() {
		if g.job != 0 {
			syscall.CloseHandle(g.job)
			g.job = 0
		}
	}()
	if ok, _, err := terminateJobObject.Call(uintptr(g.job), 1); ok == 0 {
		return fmt.Errorf("Cannot terminate the owned harness job: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		accounting := struct {
			Times                    [4]int64
			TotalPageFaultCount      uint32
			TotalProcesses           uint32
			ActiveProcesses          uint32
			TotalTerminatedProcesses uint32
		}{}
		ok, _, _ := queryJobInformation.Call(uintptr(g.job), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
		if ok == 0 {
			return errors.New("Cannot confirm owned harness job drainage.")
		}
		if accounting.ActiveProcesses == 0 {
			g.drained = true
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Owned harness job drainage is unconfirmed.")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const directoryPickerScript = `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Select project folder'
$dialog.ShowNewFolderButton = $false
$dialog.SelectedPath = [Environment]::GetFolderPath('UserProfile')
# A visible topmost owner keeps the dialog above the desktop/browser even though
# the engine itself has no window. Dispose it together with the picker.
$owner = New-Object System.Windows.Forms.Form
$owner.Text = 'Select project folder'
$owner.Size = New-Object System.Drawing.Size(1, 1)
$owner.StartPosition = [System.Windows.Forms.FormStartPosition]::CenterScreen
$owner.ShowInTaskbar = $false
$owner.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
$owner.TopMost = $true
try {
  $owner.Show()
  $owner.Activate()
  if ($dialog.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
    [Console]::Write($dialog.SelectedPath)
  }
} finally { $dialog.Dispose(); $owner.Dispose() }
`

func pickerCommand(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", directoryPickerScript)
	configureProcess(cmd)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 5 * time.Second
	return cmd, nil
}
