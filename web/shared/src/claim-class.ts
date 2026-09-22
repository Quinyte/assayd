// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

/**
 * Claim classes.
 *
 * assayd's first rule about its own text is that a bound the code does not
 * enforce must never be stated as fact (AGENTS.md rule 7). On a website that
 * rule needs a mechanism, not good intentions: every load-bearing claim on
 * either target carries the class of evidence behind it, and the class is
 * structured data rather than a turn of phrase a writer can soften.
 *
 * There are exactly three classes, and there is deliberately no fourth for
 * "partly" or "soon". A claim is proven by a named test, or it is written down
 * in a design and enforced by nothing, or nothing implements it at all.
 *
 * The resolver below THROWS on a claim that cannot name its evidence. That is
 * the point of this module: a `measured` badge with no test name is a lie, and
 * a lie should fail the build rather than render.
 */

export type ClaimClassName = "measured" | "designed" | "not-built";

export const CLAIM_CLASS_NAMES: readonly ClaimClassName[] = [
  "measured",
  "designed",
  "not-built",
] as const;

/** A named test in the repository proves this. */
export interface MeasuredClaim {
  claim: "measured";
  /**
   * The test's own identifier, verbatim, so a reader can grep for it —
   * e.g. "TestAnAgentAnswersThroughTheGateway".
   */
  test: string;
}

/** A design says this. Nothing enforces it. */
export interface DesignedClaim {
  claim: "designed";
  /** The document, e.g. "design 03". */
  design: string;
  /** The section within it, e.g. "§3.3.2". */
  section: string;
}

/** Nothing in the repository implements this. */
export interface NotBuiltClaim {
  claim: "not-built";
  /** Optional: what stands in the way, or what would have to happen first. */
  note?: string;
}

export type Claim = MeasuredClaim | DesignedClaim | NotBuiltClaim;

/** What a renderer needs, with the visual and the spoken forms separated. */
export interface ResolvedClaim {
  claim: ClaimClassName;
  /** The state word, as shown. */
  word: string;
  /** The evidence, verbatim and case-preserved, or null when there is none. */
  evidence: string | null;
  /**
   * A non-colour mark. The three classes must be distinguishable with no hue
   * at all — filled, half, empty — because colour alone never carries state
   * (WCAG 2.1 SC 1.4.1).
   */
  mark: string;
  /**
   * A complete, self-contained sentence for assistive technology. The visible
   * badge is hidden from the accessibility tree and this is announced in its
   * place, so that a screen reader hears one clear statement instead of the
   * fragments the typography is arranged from.
   */
  label: string;
}

function required(value: string | undefined, field: string, claim: string): string {
  const trimmed = (value ?? "").trim();
  if (trimmed === "") {
    throw new Error(
      `ClaimClass: a "${claim}" claim must name its "${field}". ` +
        `A badge that asserts evidence it cannot cite is exactly the failure ` +
        `the claim classes exist to prevent, so this fails the build instead ` +
        `of rendering.`,
    );
  }
  return trimmed;
}

export function resolveClaim(input: Claim): ResolvedClaim {
  switch (input.claim) {
    case "measured": {
      const test = required(input.test, "test", "measured");
      return {
        claim: "measured",
        word: "measured",
        evidence: test,
        mark: "●", // ● filled — it exists and it is checked
        label: `Claim class: measured. Proven by the test ${test}.`,
      };
    }
    case "designed": {
      const design = required(input.design, "design", "designed");
      const section = required(input.section, "section", "designed");
      return {
        claim: "designed",
        word: "designed",
        evidence: `${design} ${section}`,
        mark: "◐", // ◐ half — drawn, not built
        label:
          `Claim class: designed. Specified in ${design} ${section}, ` +
          `and enforced by no test.`,
      };
    }
    case "not-built": {
      const note = (input.note ?? "").trim();
      return {
        claim: "not-built",
        word: "not built",
        evidence: note === "" ? null : note,
        mark: "○", // ○ empty — nothing is there
        label:
          `Claim class: not built. Nothing in the repository implements this` +
          (note === "" ? "." : `: ${note}.`),
      };
    }
    default: {
      // Reached only from untyped callers (MDX, a generator emitting JSON).
      const bad = (input as { claim?: unknown }).claim;
      throw new Error(
        `ClaimClass: unknown claim class ${JSON.stringify(bad)}. ` +
          `Expected one of: ${CLAIM_CLASS_NAMES.join(", ")}.`,
      );
    }
  }
}
