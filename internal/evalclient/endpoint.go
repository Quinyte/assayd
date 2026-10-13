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
// body decodes straight into it.
type Interface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
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
		return strings.TrimSuffix(u.Path, "/") + "/message:send", nil
	}
	return "", &BindingError{msg: "the candidate's card declares no supportedInterfaces[] entry with " +
		"protocolBinding HTTP+JSON and protocolVersion 1.0, the only binding the eval speaks. " +
		"Declare one"}
}
