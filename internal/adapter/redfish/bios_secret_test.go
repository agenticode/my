package redfish

import "testing"

// TestSecretNames: an attribute is a secret by a word of its name; the
// short words key and pin count as words, not inside other words.
func TestSecretNames(t *testing.T) {
	for name, want := range map[string]bool{
		"AdminPassword": true, "SetupPasswd": true, "TpmSecret": true, "OemToken": true, "BmcCredential": true,
		"SetupPin": true, "SETUPPIN": true, "Tpm_Key": true, "SecureBootKeys": true, "PinCode": true, "KeyManagement": true,
		"MemoryMapping": false, "MemMapIo": false, "DimmSparing": false, "PatrolScrub": false, "Monkey": true,
		"MemPinning": false, "AddressKeying": false,
	} {
		if got := secret(name); got != want {
			t.Errorf("%s: secret = %v, want %v", name, got, want)
		}
	}
}
