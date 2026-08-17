//go:build windows

package secrets

import (
	"encoding/json"
	"fmt"
	"syscall"
	"unsafe"
)

// Win32 Credential Manager constants (wincred.h) - CRED_TYPE_GENERIC and
// CRED_PERSIST_LOCAL_MACHINE. Values are fixed by the Windows API, not
// something to look up at runtime.
const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	// credMaxBlobSize mirrors wincred.h's CRED_MAX_CREDENTIAL_BLOB_SIZE
	// (5 * 512 bytes) - the documented hard limit on CredentialBlobSize
	// for any credential type, CRED_TYPE_GENERIC included.
	credMaxBlobSize = 5 * 512
	// errorNotFound is ERROR_NOT_FOUND (winerror.h) - CredReadW's
	// documented result when no credential exists under TargetName; every
	// other failure is a real error, not "no credentials saved yet".
	errorNotFound = syscall.Errno(1168)
)

// filetime mirrors the Win32 FILETIME struct embedded in CREDENTIALW.
// dockvault never reads LastWritten, but the field has to be present and
// correctly sized for the rest of credentialW's layout to line up with
// what advapi32.dll expects.
type filetime struct {
	lowDateTime  uint32
	highDateTime uint32
}

// credentialW mirrors the Win32 CREDENTIALW struct (wincred.h) field for
// field, in declaration order, relying on Go's struct layout following
// the same natural (C-compatible) alignment rules as the Windows ABI on
// amd64 - the same approach golang.org/x/sys/windows uses for structs
// like this, just inlined here to avoid that dependency (see this
// package's doc comment for why).
type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr // PCREDENTIAL_ATTRIBUTEW - always nil, dockvault sets no custom attributes
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	modAdvapi32    = syscall.NewLazyDLL("advapi32.dll")
	procCredWriteW = modAdvapi32.NewProc("CredWriteW")
	procCredReadW  = modAdvapi32.NewProc("CredReadW")
	procCredFree   = modAdvapi32.NewProc("CredFree")
)

// windowsCredentialManagerStore implements Store via the Win32 Credential
// Manager API (CredWriteW/CredReadW/CredFree in advapi32.dll), called
// through the standard library's own syscall package (NewLazyDLL/NewProc)
// rather than golang.org/x/sys/windows - this project has an explicit
// zero-external-Go-dependency policy (see go.mod/README), and raw syscall
// is sufficient for the three functions this needs.
//
// One Credential Manager entry per job: TargetName "DockVault/<job-name>",
// CRED_TYPE_GENERIC, CRED_PERSIST_LOCAL_MACHINE (survives reboots, scoped
// to the Windows account dockvault runs as - not roaming, not
// session-only). The job's entire credentials map (+ webhook URL, same
// bundling as env_unix.go's .env file) is JSON-marshaled into a single
// CredentialBlob. CredWriteW overwrites an existing same-named entry per
// its documented behavior, so Save is idempotent with no separate
// delete-then-write step.
//
// KNOWN LIMITATION (see DOCKVAULT_CONTEXT.md's "Known Limitations" for
// the full writeup): Credential Manager is a per-user, DPAPI-backed
// store. A `dockvault backup` invoked by Task Scheduler may run in a
// session that can't unlock it, depending on how the task is configured
// ("run only when user is logged on" vs. "run whether user is logged on
// or not"). Load's error message on a real read failure (as opposed to
// "not found", which just means no credentials were ever saved) says so
// explicitly rather than surfacing an opaque Win32 error code.
//
// UNVERIFIED AT RUNTIME: written and cross-compiled (GOOS=windows
// GOARCH=amd64) from a Linux-only development environment with no
// Windows machine available - go build/go vet confirm this compiles and
// type-checks, but CredWriteW/CredReadW have never actually been called
// for real. Treat this as reviewed, not battle-tested, until it's
// exercised on the real deployment target.
type windowsCredentialManagerStore struct{}

// New returns the platform's Store implementation - Windows Credential
// Manager, the only GOOS this file is compiled for.
func New() Store {
	return windowsCredentialManagerStore{}
}

func targetName(jobName string) string {
	return "DockVault/" + jobName
}

func (windowsCredentialManagerStore) Save(jobName string, values map[string]string) error {
	blob, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encoding credentials for %q: %w", jobName, err)
	}
	if len(blob) > credMaxBlobSize {
		return fmt.Errorf("credentials for %q are too large for Windows Credential Manager (%d bytes, limit %d) - this shouldn't happen for ordinary DB credentials", jobName, len(blob), credMaxBlobSize)
	}

	targetPtr, err := syscall.UTF16PtrFromString(targetName(jobName))
	if err != nil {
		return fmt.Errorf("encoding credential target name for %q: %w", jobName, err)
	}
	commentPtr, err := syscall.UTF16PtrFromString("DockVault job credentials")
	if err != nil {
		return fmt.Errorf("encoding credential comment for %q: %w", jobName, err)
	}
	// Some Windows versions reject a CRED_TYPE_GENERIC write with a null
	// UserName despite MSDN documenting it as optional for that type - a
	// fixed placeholder avoids that version-dependent failure mode.
	userPtr, err := syscall.UTF16PtrFromString("dockvault")
	if err != nil {
		return fmt.Errorf("encoding credential username for %q: %w", jobName, err)
	}

	cred := credentialW{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		Comment:            commentPtr,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}

	r1, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r1 == 0 {
		return fmt.Errorf("writing %q to Windows Credential Manager: %w", jobName, callErr)
	}
	return nil
}

func (windowsCredentialManagerStore) Load(jobName string) (map[string]string, bool, error) {
	targetPtr, err := syscall.UTF16PtrFromString(targetName(jobName))
	if err != nil {
		return nil, false, fmt.Errorf("encoding credential target name for %q: %w", jobName, err)
	}

	var pcred *credentialW
	r1, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pcred)),
	)
	if r1 == 0 {
		if callErr == errorNotFound {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading credentials for %q from Windows Credential Manager: %w\n"+
			"  hint: Credential Manager is a per-user store - if dockvault is running as a scheduled task, "+
			"confirm Task Scheduler is configured to run with access to this user's credential vault "+
			"(e.g. \"Run only when user is logged on\"), or test the task interactively first", jobName, callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pcred)))

	if pcred == nil || pcred.CredentialBlobSize == 0 {
		return map[string]string{}, true, nil
	}
	// Copy out of the OS-owned buffer into Go-managed memory before the
	// deferred CredFree above releases it.
	src := unsafe.Slice(pcred.CredentialBlob, pcred.CredentialBlobSize)
	data := make([]byte, len(src))
	copy(data, src)

	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, false, fmt.Errorf("decoding credentials for %q from Windows Credential Manager: %w", jobName, err)
	}
	return values, true, nil
}
