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
// So is a path the eval does not admit: one with any percent-escape, or with
// a byte outside ASCII letters, digits and
// "-" "." "_" "~" "/" ":" "@" "$" "&" "+" "," ";" "=". The eval records the
// path decoded, and only such a path is both recorded and sent exactly as the
// card wrote it. §1.1 does not list this refusal either (#105 item 8).
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
		// The eval records the path decoded (status.eval.endpointPath) and
		// sends it with url.URL's default escaping, so it admits only a path
		// that both leave as the card wrote it: one written from ASCII letters,
		// digits and pathPunctuation, with no percent-escape (#105 item 8).
		// Two checks give that. An escape of a byte inside the allowed set
		// differs from url.URL's default escaping of the decoded path, so
		// url.Parse sets RawPath, and the first check refuses it. An escape
		// of a byte outside the set decodes to that byte, and
		// pathSendsAsWritten refuses it. So an admitted path holds no escape
		// and only bytes url.URL puts on the request line unescaped: it is
		// recorded and sent exactly as written.
		// TestEveryByteIsSentAsWrittenOrRefused holds that to what Send puts
		// on the wire. Other paths are not all unsendable — "/a%20b/" would
		// go out as written — but the eval could not record them as written,
		// and a path it records is the path it sends.
		if u.RawPath != "" || !pathSendsAsWritten(u.Path) {
			return "", &BindingError{msg: fmt.Sprintf(
				"the candidate's card declares HTTP+JSON 1.0 at supportedInterfaces[%d] with a url whose "+
					"path is not one the eval admits: it admits only a path written with ASCII letters, digits "+
					"and %s, with no percent-escape. Serve the interface at such a path", i, quotedPunctuation())}
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

// pathPunctuation is every byte other than a letter or a digit that a card's
// path may be written with: ones url.URL puts on the request line unescaped.
const pathPunctuation = "-._~/:@$&+,;="

// quotedPunctuation is pathPunctuation as the refusal lists it.
func quotedPunctuation() string {
	q := make([]string, 0, len(pathPunctuation))
	for i := 0; i < len(pathPunctuation); i++ {
		q = append(q, `"`+pathPunctuation[i:i+1]+`"`)
	}
	return strings.Join(q, " ")
}

// pathSendsAsWritten reports whether every byte of p is a letter, a digit,
// or in pathPunctuation.
func pathSendsAsWritten(p string) bool {
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte(pathPunctuation, c) >= 0:
		default:
			return false
		}
	}
	return true
}
