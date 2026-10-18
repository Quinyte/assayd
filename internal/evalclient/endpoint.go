// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalclient

import (
	"fmt"
	"net/url"
	"strings"
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
// So is a path the client would not send exactly as written: one with any
// percent-escape, or with a byte outside letters, digits and
// "-" "." "_" "~" "/" ":" "@" "$" "&" "+" "," ";" "=". §1.1 does not list this
// refusal either (#105 item 8).
//
// An entry that names a tenant is refused too, which design 16 §1.1 does not
// list; the human decided on 2026-10-18 to keep the refusal (#108).
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
		// The path is admitted only if the client will send it exactly as the
		// card wrote it: no percent-escape anywhere in it (RawPath is empty,
		// so Path is what was written), and every byte in pathAllowed, the
		// bytes url.URL puts on the request line unescaped. Anything else —
		// an escape, which Path decodes and the client re-encodes its own
		// way; a byte the client escapes, such as "!", a space or any byte
		// from 0x80 — would send the candidate a path its card does not
		// declare, and fail it for good as a ProtocolError. Refused instead
		// (#105 item 8). TestEveryByteIsSentAsWrittenOrRefused holds the set
		// to what Send puts on the wire.
		if u.RawPath != "" || !pathSendsAsWritten(u.Path) {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a url whose "+
					"path holds a character or an escape the eval client cannot send exactly as written. "+
					"Write the path with only letters, digits, \"-\" \".\" \"_\" \"~\" \"/\" \":\" \"@\" "+
					"\"$\" \"&\" \"+\" \",\" \";\" \"=\", and no percent-escapes", i)}
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

// pathSendsAsWritten reports whether every byte of p is one url.URL puts on
// the request line as it is: ALPHA / DIGIT / "-" "." "_" "~" "/" ":" "@" "$"
// "&" "+" "," ";" "=".
func pathSendsAsWritten(p string) bool {
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("-._~/:@$&+,;=", c) >= 0:
		default:
			return false
		}
	}
	return true
}
