---
name: write-spec
description: Rules for anything a human reads to understand assayd — designs, ADRs, CRD schemas, error messages, condition names. Use when writing or revising a design doc, an ADR, a CRD type, or any user-facing string. A spec that needs a person to already know the answer has failed.
paths: docs/**, api/**, config/**, charts/**
---

A specification is read by someone who does not yet know the answer. Write for that person.

## Every spec

- **Self-contained.** A reader lands mid-document. Name the thing before using it; expand an acronym once per document; link the design or ADR that owns a concept rather than assuming it was read.
- **State the rule, then the reason.** "There is no namespace field" is a fact; "because design 24 derives the grant from the binding, so a cross-namespace reference would authorize itself" is what stops someone adding it back next quarter.
- **Say what happens, not what is intended.** "Admission rejects unsigned images" beats "images should be signed." If nothing enforces it, write "nothing enforces this yet" — an unenforced promise read as a guarantee is how the sandbox rule sat unimplemented for a whole design cycle.
- **Examples are the spec's test.** Every schema gets a minimal example and a realistic one. If you cannot write the minimal one in a few lines, the schema is the problem, not the example.
- **Never delete evidence against your own change.** If a comment or line contradicts what you are about to write, address it in the text. Removing it and proceeding is how a false premise got into ADR-0027 and survived until an independent critic grepped for it.

## CRD schemas are specs (ADR-0027)

- **Nesting must select between alternatives.** `expose: {a2a: …}` earns its wrapper: another arm is coming. A wrapper naming a protocol rather than a choice is ceremony — delete it and inline the fields.
- **One vocabulary.** A concept keeps its name in every CRD. Two names for one idea is a thing every reader must learn twice.
- **Required is a demand.** Everything except the one unavoidable field is optional or defaulted, and the API server applies the default, not the operator. Requiring an object is as much a demand as requiring a string.
- **A reference resolves in the object's own namespace** unless a design specifies a consent handshake. Grants derived from references make an accepted reference an accepted grant.
- **Errors name the fix.** Not "invalid combination" but "spec.runtime.sandbox is a stateful singleton: set replicas to 1, or drop sandbox to scale out." Prefer CEL on the schema over a webhook: it is caught at apply time and costs no pod.
- **Conditions and phases are vocabulary.** `[]metav1.Condition` accepts any string, so the list lives in one named place with a test asserting it. A silent condition is worse than a noisy one — NFR-8.

## Prose

Short sentences carrying one fact each. Tables for anything with more than three parallel cases. Concrete numbers over "several" or "many". No hedging on a decided question: if it is decided, state it; if it is open, put it in the open-items list where someone will find it.

## Refuse to finish

- a rule stated in prose with nothing enforcing it, and no note saying so
- an example that does not parse, or that the current schema would reject
- a cross-reference to a section number that has moved
- a doc amended to match code without the amendment being recorded

Amendments are numbered and kept. Review records — `docs/designs/reviews/**` — are history: never edit them to match a later decision.
