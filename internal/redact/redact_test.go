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
set protocols bgp group PEER neighbor 10.0.0.2 authentication-key "junosBgpSecret"

! Fortinet
set password ENC 1234567890
set preshared-key my-ipsec-psk
set psksecret ENC fortiPskSecret123
set auth-psk "fortiAuthPskSecret"
set community-name "fortiSnmpSecret"

! Routing Protocols & Check Point
neighbor 10.1.1.2 password ciscoBgpSecretPass
ip ospf message-digest-key 1 md5 ospfMd5SecretPass
set user admin password-hash $1$gaia$99887766554433221100
password plaintext arubaSecretPass

! TACACS & RADIUS
tacacs-server key 7 secretTacacsPass
radius-server host 10.1.1.1 key radiusSecretPass

! SNMPv3
snmp-server user admin network-admin v3 auth sha MyAuthPass123 priv aes 128 MyPrivPass456

! VyOS SNMP & User Login
set service snmp community vyosCommunityRO client 10.0.0.0/8
set system login user netadmin authentication encrypted-password '$6$rounds=5000$vyosSecretHash'
set system login user netadmin level 'admin'

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
		"fortiPskSecret123",
		"fortiAuthPskSecret",
		"fortiSnmpSecret",
		"secretTacacsPass",
		"radiusSecretPass",
		"MyAuthPass123",
		"MyPrivPass456",
		"vyosCommunityRO",
		"$6$rounds=5000$vyosSecretHash",
		"junosBgpSecret",
		"ciscoBgpSecretPass",
		"ospfMd5SecretPass",
		"$1$gaia$99887766554433221100",
		"arubaSecretPass",
	}

	for _, str := range forbidden {
		if strings.Contains(redacted, str) {
			t.Errorf("expected string %q to be redacted, but found in output:\n%s", str, redacted)
		}
	}

	if !strings.Contains(redacted, "set system login user netadmin level 'admin'") {
		t.Errorf("expected subsequent commands to be preserved, but missing from output:\n%s", redacted)
	}

	if !strings.Contains(redacted, "<REDACTED>") {
		t.Errorf("expected <REDACTED> marker in output, got:\n%s", redacted)
	}
}
