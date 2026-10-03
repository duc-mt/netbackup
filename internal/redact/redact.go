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
		re:   regexp.MustCompile(`(?i)(^\s*(?:enable\s+)?(?:password|secret)(?:\s+\d+)?)\s+\S+`),
		repl: "${1} <REDACTED>",
	},
	// Cisco / Network SNMP community strings
	{
		re:   regexp.MustCompile(`(?i)(^\s*snmp-server\s+community)\s+\S+(.*)`),
		repl: "${1} <REDACTED>${2}",
	},
	// IPsec / VPN pre-shared keys (Cisco, Check Point, etc.)
	{
		re:   regexp.MustCompile(`(?i)(^\s*(?:pre-shared-key|preshared-key|key)\s+(?:0|5|6|7)?)\s*\S+`),
		repl: "${1} <REDACTED>",
	},
	// Juniper Junos encrypted passwords & secrets
	{
		re:   regexp.MustCompile(`(?i)(encrypted-password|pre-shared-secret)\s+"?[^";]+"?`),
		repl: `${1} "<REDACTED>"`,
	},
	// Juniper SNMP communities
	{
		re:   regexp.MustCompile(`(?i)(community\s+)"?[^";{\s]+"?(\s*\{)`),
		repl: `${1}"<REDACTED>"${2}`,
	},
	// FortiOS passwords and keys (e.g. set password ENC ..., set preshared-key ...)
	{
		re:   regexp.MustCompile(`(?i)(^\s*set\s+(?:password|secret|preshared-key|pre-shared-key|private-key))\s+\S+`),
		repl: `${1} "<REDACTED>"`,
	},
	// Huawei VRP cipher passwords / communities
	{
		re:   regexp.MustCompile(`(?i)(^\s*(?:password|cipher|snmp-agent community (?:read|write)))\s+cipher\s+\S+`),
		repl: `${1} cipher <REDACTED>`,
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
