// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// taskCompleted is TaskState's terminal success, by its proto name, which is
// how protojson writes an enum (a2a.proto at v1.0.1).
const taskCompleted = "TASK_STATE_COMPLETED"

// score is design 16 §1.1 step 6's table for a 200 whose body was read whole.
//
// The members on the answer path — task or message, task.status.state, and
// the parts the answer text is taken from — are read as protojson writes
// them, and strictly, not through encoding/json's struct decoding: that
// matches keys case-insensitively, so {"Task": …} would be read as a task, and
// it merges a duplicate object key into the first, so a TASK_STATE_FAILED task
// carrying the expected answer, followed by a second "task" that says only
// COMPLETED, would pass. Here keys match exactly, a duplicate key anywhere in
// the body is ProtocolError, and so is a member on the path of the wrong JSON
// type, and a text part beside another member of its oneof.
//
// The body must be valid UTF-8. Within a string, encoding/json reads an
// escaped lone surrogate (\ud800) as U+FFFD where protojson refuses it, so
// such a text is scored as if it held U+FFFD. A part with no text is checked
// only for being an object; its other members are not read.
//
// The rest of the body is not validated. A key the client does not read is
// ignored, as a reader of a newer minor version must, and so is the value of
// a field off the answer path: a task whose status.timestamp is not a
// timestamp, which protojson would refuse, is scored here. So step 6's "a body
// that is not a SendMessageResponse" is implemented as a body whose answer
// path does not read as one (#105).
func score(raw []byte, expect assaydv1alpha1.EvalExpect) Outcome {
	protocolError := Outcome{Reason: ReasonProtocolError}
	top, ok := object(raw)
	if !ok || !utf8.Valid(raw) || !noDuplicateKeys(raw) {
		return protocolError
	}
	// SendMessageResponse is a oneof: exactly one of task and message.
	task, message := present(top["task"]), present(top["message"])
	var parts []json.RawMessage
	switch {
	case task != nil && message == nil:
		t, ok := object(task)
		if !ok {
			return protocolError
		}
		state, ok := taskState(t)
		if !ok {
			return protocolError
		}
		if state != taskCompleted {
			return Outcome{Reason: ReasonTaskNotCompleted}
		}
		artifacts, ok := list(t["artifacts"])
		if !ok {
			return protocolError
		}
		for _, a := range artifacts {
			ao, ok := object(a)
			if !ok {
				return protocolError
			}
			p, ok := list(ao["parts"])
			if !ok {
				return protocolError
			}
			parts = append(parts, p...)
		}
	case message != nil && task == nil:
		m, ok := object(message)
		if !ok {
			return protocolError
		}
		if parts, ok = list(m["parts"]); !ok {
			return protocolError
		}
	default:
		return protocolError
	}
	texts, ok := textParts(parts)
	if !ok {
		return protocolError
	}
	if satisfies(strings.Join(texts, " "), expect) {
		return Outcome{Passed: true}
	}
	return Outcome{Reason: ReasonMismatch}
}

// taskState is task.status.state, or "" when either is absent.
func taskState(task map[string]json.RawMessage) (string, bool) {
	status := present(task["status"])
	if status == nil {
		return "", true
	}
	s, ok := object(status)
	if !ok {
		return "", false
	}
	var state string
	if v := present(s["state"]); v != nil {
		if err := json.Unmarshal(v, &state); err != nil {
			return "", false
		}
	}
	return state, true
}

// textParts is the text of every text part, in order. Only a text part
// contributes to the answer, and a text part may be empty. A part that sets
// text and another member of its oneof is not a Part. raw and url are a bytes
// and a string field, so a null there is unset; data is a
// google.protobuf.Value, whose null is a value, so a data key beside text is
// a second member whatever it holds.
func textParts(parts []json.RawMessage) ([]string, bool) {
	var texts []string
	for _, p := range parts {
		po, ok := object(p)
		if !ok {
			return nil, false
		}
		v := present(po["text"])
		if v == nil {
			continue
		}
		if _, data := po["data"]; data || present(po["raw"]) != nil || present(po["url"]) != nil {
			return nil, false
		}
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			return nil, false
		}
		texts = append(texts, text)
	}
	return texts, true
}

// object decodes one JSON object into its members, keyed exactly as written.
func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil || o == nil {
		return nil, false
	}
	return o, true
}

// list decodes a JSON array, absent or null being empty.
func list(raw json.RawMessage) ([]json.RawMessage, bool) {
	if present(raw) == nil {
		return nil, true
	}
	var l []json.RawMessage
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false
	}
	return l, true
}

// present is raw, or nil when the member is absent or null: protojson writes
// an unset field as absent, and reads a null as unset for every field on the
// answer path. (A google.protobuf.Value field is the exception, where null is
// a value; textParts handles the one such field it meets.)
func present(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// noDuplicateKeys reports whether no object anywhere in raw, which is valid
// JSON, repeats a key.
func noDuplicateKeys(raw []byte) bool {
	type frame struct {
		keys map[string]bool // nil for an array
		key  bool            // the next string token in this object is a key
	}
	var stack []*frame
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A number is kept as written, so one too large for a float64, in a field
	// the client never reads, is not an error here.
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			// Unreachable while raw is valid JSON, which score checks first.
			return false
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if s, ok := tok.(string); ok && top != nil && top.keys != nil && top.key {
			if top.keys[s] {
				return false
			}
			top.keys[s], top.key = true, false
			continue
		}
		// Any other token is a value, or the start or end of one. In an
		// object, what follows a value is a key.
		if top != nil && top.keys != nil {
			top.key = true
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, &frame{keys: map[string]bool{}, key: true})
		case json.Delim('['):
			stack = append(stack, &frame{})
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
	}
}
