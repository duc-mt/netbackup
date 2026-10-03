package redact

import (
	"strings"
	"testing"
)

func TestRedactConfig(t *testing.T) {
	input := `! Cisco IOS
enable secret 5 $1$mERr$hx5rVt7rPNoS4wqbXKXqk1
username admin privilege 15 secret 9 $9$jK8...
username backup password 7 08324F5E1A
snmp-server community public RO
snmp-server community private RW
crypto isakmp key mySecretKey123 address 192.168.1.1

! Juniper
set system root-authentication encrypted-password "$6$rounds=10000$..."
set snmp community "public" {
set security ike proposal p1 pre-shared-secret "$9$..."

! Fortinet
set password ENC 1234567890
set preshared-key my-ipsec-psk

! Private Key
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0Y...
-----END RSA PRIVATE KEY-----
`

	redacted := Config(input)

	forbidden := []string{
		"$1$mERr$hx5rVt7rPNoS4wqbXKXqk1",
		"08324F5E1A",
		"public",
		"private",
		"mySecretKey123",
		"MIIEowIBAAKCAQEA0Y...",
		"1234567890",
		"my-ipsec-psk",
	}

	for _, str := range forbidden {
		if strings.Contains(redacted, str) {
			t.Errorf("expected string %q to be redacted, but found in output:\n%s", str, redacted)
		}
	}

	if !strings.Contains(redacted, "<REDACTED>") {
		t.Errorf("expected <REDACTED> marker in output, got:\n%s", redacted)
	}
}
