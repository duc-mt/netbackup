// ==============================================================================
// Package main implements Implementation and logic for redact..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run redact.go [options]
// Notes:         Go package implementation
// ==============================================================================
// Package redact provides sensitive data sanitization for network device
// configurations, masking passwords, hashes, pre-shared keys, and SNMP
// community strings before saving configurations to disk.
package redact

import (
	"regexp"
)

type patternRule struct {
	re   *regexp.Regexp
	repl string
}

var commonPatterns = []patternRule{
	// Private key blocks (RSA, EC, OPENSSH, etc.)
	{
		re:   regexp.MustCompile(`(?s)-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----.*?-----END[ A-Z0-9_-]*PRIVATE KEY-----`),
		repl: "-----BEGIN PRIVATE KEY-----\n<REDACTED>\n-----END PRIVATE KEY-----",
	},
	// Cisco / IOS-XE / Ruijie / Aruba password and secret definitions
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*(?:username\s+\S+(?:\s+privilege\s+\d+)?\s+)?(?:enable\s+)?(?:password|secret)(?:\s+\d+)?)\s+\S+`),
		repl: "${1} <REDACTED>",
	},
	// Cisco / Network SNMP community strings
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*snmp-server\s+community)\s+\S+(.*)`),
		repl: "${1} <REDACTED>${2}",
	},
	// IPsec / VPN pre-shared keys (Cisco, Check Point, etc.)
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*(?:crypto\s+isakmp\s+key|pre-shared-key|preshared-key|key)\s+(?:0|5|6|7)?)\s*\S+`),
		repl: "${1} <REDACTED>",
	},
	// Juniper Junos encrypted passwords & secrets
	{
		re:   regexp.MustCompile(`(?i)(?m)(encrypted-password|pre-shared-secret)\s+"?[^";]+"?`),
		repl: `${1} "<REDACTED>"`,
	},
	// Juniper SNMP communities
	{
		re:   regexp.MustCompile(`(?i)(?m)(community\s+)"?[^";{\s]+"?(\s*\{)`),
		repl: `${1}"<REDACTED>"${2}`,
	},
	// FortiOS passwords and keys (e.g. set password ENC ..., set preshared-key ...)
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*set\s+(?:password(?:\s+ENC)?|secret|preshared-key|pre-shared-key|private-key))\s+\S+`),
		repl: `${1} "<REDACTED>"`,
	},
	// Huawei VRP cipher passwords / communities
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*(?:password|cipher|snmp-agent community (?:read|write)))\s+cipher\s+\S+`),
		repl: `${1} cipher <REDACTED>`,
	},
	// TACACS+ and RADIUS server keys
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*(?:tacacs-server|radius-server|tacacs|radius)(?:(?:\s+\S+)*?\s+key(?:\s+\d+)?))\s+\S+`),
		repl: "${1} <REDACTED>",
	},
	// SNMPv3 auth and priv credentials
	{
		re:   regexp.MustCompile(`(?i)(\bauth\s+(?:md5|sha|sha-256|sha-384|sha-512)\s+)\S+`),
		repl: "${1}<REDACTED>",
	},
	{
		re:   regexp.MustCompile(`(?i)(\bpriv\s+(?:des|3des|aes|aes-128|aes-192|aes-256)(?:\s+\d+)?\s+)\S+`),
		repl: "${1}<REDACTED>",
	},
	// VyOS SNMP community strings
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*set\s+service\s+snmp\s+community\s+)\S+(.*)`),
		repl: "${1}<REDACTED>${2}",
	},
	// FortiOS SNMP community strings
	{
		re:   regexp.MustCompile(`(?i)(?m)(^\s*set\s+community-name\s+)"?[^"\r\n]+"?`),
		repl: `${1}"<REDACTED>"`,
	},
}

// Config processes raw configuration text and replaces recognized sensitive
// credentials, hashes, and community strings with redacted placeholders.
func Config(content string) string {
	result := content
	for _, rule := range commonPatterns {
		result = rule.re.ReplaceAllString(result, rule.repl)
	}
	return result
}
