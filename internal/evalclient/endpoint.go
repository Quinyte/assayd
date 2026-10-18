// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalclient

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ReasonBindingUnsupported is the status.eval.blocked reason for a candidate
// card the eval cannot take an endpoint from (design 16 §1.1 step 2).
const ReasonBindingUnsupported = "EvalBindingUnsupported"

// Interface is one entry of an A2A 1.0 card's supportedInterfaces[], as far
// as the eval reads it. The tags are the card's protojson names, so a card
// body decodes straight into it — through encoding/json, which matches keys
// case-insensitively and keeps the last of a duplicate, so a card with both
// "tenant" and "TENANT" is read by the last of them.
type Interface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
	// Tenant, when set, MUST be sent with every request to the interface
	// (a2a.proto at v1.0.1, AgentInterface.tenant). The eval sends none.
	Tenant string `json:"tenant"`
}

// BindingError is a card the eval cannot send a case to. Its Reason is
// EvalBindingUnsupported, and its message never quotes the card's URL: that is
// an address the agent chose, and status is no place to echo it.
type BindingError struct{ msg string }

func (e *BindingError) Error() string { return e.msg }

// Reason is the status.eval.blocked reason the error is written under.
func (e *BindingError) Reason() string { return ReasonBindingUnsupported }

// EndpointPath is the path every case of a run is sent to, chosen once at run
// start from the candidate's card (design 16 §1.1 step 2).
//
// It takes the first entry whose protocolBinding is HTTP+JSON and whose
// protocolVersion is 1.0. From that entry's URL it takes the path alone,
// trims one trailing "/", and appends "/message:send": "" and "/" give
// "/message:send", "/a2a" and "/a2a/" give "/a2a/message:send". The scheme,
// host and port are dropped, because the case goes to the candidate's Service
// and never to an address the card names (step 3).
//
// A URL that does not parse, or that carries a query or a fragment, is
// refused, as is a card with no such entry. The first matching entry is the
// one judged: a refused entry is not passed over for a later one.
//
// An entry that names a tenant is refused too, which design 16 §1.1 does not
// list: #108 asks the human whether to keep this refusal or send the tenant.
// A2A requires a client to send an interface's tenant with every request, and
// the eval's SendMessageRequest carries none, so sending would either be
// refused by an honest agent — failing the candidate, finally, for the eval's
// own omission — or be routed to some other tenant, whose answers the verdict
// would then certify. A refusal holds the candidate instead, as the other
// refusals do, and the reason says why.
func EndpointPath(interfaces []Interface) (string, error) {
	for i, in := range interfaces {
		if in.ProtocolBinding != "HTTP+JSON" || in.ProtocolVersion != "1.0" {
			continue
		}
		u, err := url.Parse(in.URL)
		if err != nil {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d], and its url does "+
					"not parse, so no endpoint can be taken from it. Serve a card whose first HTTP+JSON 1.0 "+
					"interface has a plain URL", i)}
		}
		// ForceQuery is a bare "?"; a "#" is always a fragment delimiter, even
		// an empty one, which url.URL keeps no trace of.
		if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(in.URL, "#") {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a query or a "+
					"fragment in its url. The eval sends each case to the URL's path alone, so it cannot "+
					"honour either; serve the interface at a URL with neither", i)}
		}
		// The path is checked as decoded first, so that every spelling of one
		// byte gets one refusal: "%0a" and "%0A" alike are a control
		// character, "%ff" and "%FF" alike are not UTF-8.
		if !utf8.ValidString(u.Path) {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a url whose "+
					"path decodes to bytes that are not valid UTF-8, which status.eval cannot record as "+
					"they are. Serve the interface at a path that decodes to UTF-8", i)}
		}
		if strings.ContainsFunc(u.Path, unicode.IsControl) {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a url whose "+
					"path decodes to a control character. Serve the interface at a path without one", i)}
		}
		// Then the path as written against the path the client puts on the
		// wire, WirePath of the decoded path. They may differ only in what RFC
		// 3986 §6.2.2 calls equivalent: the case of percent-encoded hex, and
		// percent-encoded unreserved characters. Anything else — an escaped
		// slash, "%2F", which the client sends as "/"; or "!", "*", "(", ")",
		// which it sends as "%21", "%2A", "%28", "%29", not equivalent under §2.2
		// — would send the candidate a path its card never declared, failed
		// for good as a ProtocolError. Refused instead (#105 item 8).
		if normalizePath(u.EscapedPath()) != normalizePath(WirePath(u.Path)) {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a url whose "+
					"path, as written, is not the path the eval client would send: the client re-encodes "+
					"it, so the candidate would be sent a path its card does not declare. Write the path "+
					"without percent-escapes of reserved characters, such as an escaped slash, and "+
					"without characters the client re-encodes, such as \"!\", \"*\", \"(\" and \")\"", i)}
		}
		if in.Tenant != "" {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a tenant, "+
					"which A2A requires on every request to that interface. The eval sends no tenant, so "+
					"it cannot call this interface. Serve a card whose first HTTP+JSON 1.0 interface names no "+
					"tenant", i)}
		}
		return strings.TrimSuffix(u.Path, "/") + "/message:send", nil
	}
	return "", &BindingError{msg: "the candidate's card declares no supportedInterfaces[] entry with " +
		"protocolBinding HTTP+JSON and protocolVersion 1.0, the only binding the eval speaks. " +
		"Declare one"}
}

// WirePath is the path, as the request line carries it, of a request Send
// builds for a case whose Path is path: Send builds its URL from Path alone
// (Case.target), and url.URL renders that with its default escaping.
// TestWirePathIsWhatSendPutsOnTheWire holds the two together.
func WirePath(path string) string {
	return (&url.URL{Path: path}).EscapedPath()
}

// normalizePath is RFC 3986 §6.2.2's percent-encoding normalization: the hex
// of every percent-encoding uppercased, and a percent-encoded unreserved
// character (ALPHA / DIGIT / "-" / "." / "_" / "~") decoded. Two paths that
// normalize alike name the same resource.
func normalizePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '%' || i+2 >= len(p) || !isHex(p[i+1]) || !isHex(p[i+2]) {
			b.WriteByte(p[i])
			continue
		}
		hex := strings.ToUpper(p[i+1 : i+3])
		c := unhex(hex[0])<<4 | unhex(hex[1])
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + hex)
		}
		i += 2
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	default:
		return c - 'A' + 10
	}
}

func isUnreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}
