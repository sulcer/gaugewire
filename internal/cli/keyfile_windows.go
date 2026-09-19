//go:build windows

package cli

// keyFileWarning reports nothing: Windows access is governed by ACLs, which
// the Unix permission bits this check reads do not describe.
func keyFileWarning(string) string { return "" }
