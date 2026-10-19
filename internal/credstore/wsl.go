package credstore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/pavp/tkt/internal/execx"
)

// WSL stores credentials in Windows Credential Manager through powershell.exe. The value travels on stdin,
// never in arguments, so it does not show up in process listings.
type WSL struct {
	run     execx.Runner
	timeout time.Duration
}

func NewWSL(run execx.Runner) *WSL {
	if run == nil {
		run = execx.OS{}
	}
	return &WSL{run: run, timeout: 30 * time.Second}
}

func (w *WSL) Name() string { return "Windows Credential Manager" }

// notFoundExit is the exit code the read script uses when the credential does not exist.
const notFoundExit = 3

var safeTarget = regexp.MustCompile(`^[A-Za-z0-9:._@+-]+$`)

func target(key string) (string, error) {
	t := service + ":" + key
	if !safeTarget.MatchString(t) {
		return "", fmt.Errorf("credential key %q has unsupported characters", key)
	}
	return t, nil
}

func (w *WSL) Get(key string) (string, error) {
	t, err := target(key)
	if err != nil {
		return "", err
	}
	out, err := w.powershell(fmt.Sprintf(`$v = [TktCred]::Read('%s'); if ($null -eq $v) { exit %d }; [Console]::Out.Write($v)`, t, notFoundExit), nil)
	if err != nil {
		if strings.Contains(err.Error(), fmt.Sprintf("exit status %d", notFoundExit)) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read %s: %w", w.Name(), err)
	}
	return string(out), nil
}

func (w *WSL) Set(key, value string) error {
	t, err := target(key)
	if err != nil {
		return err
	}
	if _, err := w.powershell(fmt.Sprintf(`[TktCred]::Write('%s', [Console]::In.ReadToEnd())`, t), []byte(value)); err != nil {
		return fmt.Errorf("write %s: %w", w.Name(), err)
	}
	return nil
}

func (w *WSL) Delete(key string) error {
	t, err := target(key)
	if err != nil {
		return err
	}
	out, err := w.powershell(fmt.Sprintf(`[Console]::Out.Write([TktCred]::Delete('%s'))`, t), nil)
	if err != nil {
		return fmt.Errorf("delete from %s: %w", w.Name(), err)
	}
	if strings.TrimSpace(string(out)) != "True" {
		return ErrNotFound
	}
	return nil
}

func (w *WSL) powershell(body string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	script := "$ErrorActionPreference = 'Stop'\n" + credType + "\n" + body
	args := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encode(script)}
	out, err := w.run.Run(ctx, "", "powershell.exe", args, stdin)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("powershell.exe did not answer within %s", w.timeout)
	}
	return out, err
}

// encode produces the UTF-16LE base64 form that -EncodedCommand expects.
func encode(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		b[2*i] = byte(r)
		b[2*i+1] = byte(r >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

const credType = `Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class TktCred {
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct CREDENTIAL {
    public int Flags; public int Type; public string TargetName; public string Comment;
    public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
    public int CredentialBlobSize; public IntPtr CredentialBlob; public int Persist;
    public int AttributeCount; public IntPtr Attributes; public string TargetAlias; public string UserName;
  }
  [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool CredRead(string target, int type, int flags, out IntPtr cred);
  [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool CredWrite(ref CREDENTIAL cred, int flags);
  [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool CredDelete(string target, int type, int flags);
  [DllImport("advapi32.dll")]
  static extern void CredFree(IntPtr cred);
  public static string Read(string target) {
    IntPtr p;
    if (!CredRead(target, 1, 0, out p)) { return null; }
    try {
      CREDENTIAL c = (CREDENTIAL)Marshal.PtrToStructure(p, typeof(CREDENTIAL));
      return Marshal.PtrToStringUni(c.CredentialBlob, c.CredentialBlobSize / 2);
    } finally { CredFree(p); }
  }
  public static void Write(string target, string value) {
    byte[] bytes = Encoding.Unicode.GetBytes(value.TrimEnd('\r', '\n'));
    CREDENTIAL c = new CREDENTIAL();
    c.Type = 1; c.TargetName = target; c.UserName = "tkt"; c.Persist = 2;
    c.CredentialBlobSize = bytes.Length;
    c.CredentialBlob = Marshal.AllocHGlobal(bytes.Length);
    try {
      Marshal.Copy(bytes, 0, c.CredentialBlob, bytes.Length);
      if (!CredWrite(ref c, 0)) { throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
    } finally { Marshal.FreeHGlobal(c.CredentialBlob); }
  }
  public static bool Delete(string target) { return CredDelete(target, 1, 0); }
}
"@`
