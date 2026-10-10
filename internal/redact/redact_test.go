// ==============================================================================
// Package main implements Implementation and logic for redact_test..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run redact_test.go [options]
// Notes:         Go package implementation
// ==============================================================================
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
set community-name "fortiSnmpSecret"

! TACACS & RADIUS
tacacs-server key 7 secretTacacsPass
radius-server host 10.1.1.1 key radiusSecretPass

! SNMPv3
snmp-server user admin network-admin v3 auth sha MyAuthPass123 priv aes 128 MyPrivPass456

! VyOS SNMP
set service snmp community vyosCommunityRO client 10.0.0.0/8

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
		"fortiSnmpSecret",
		"secretTacacsPass",
		"radiusSecretPass",
		"MyAuthPass123",
		"MyPrivPass456",
		"vyosCommunityRO",
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
