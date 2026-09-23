// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0
import { defineConfig } from "astro/config";

// assayd.io — the site.
//
// The deploy target is a plain static host: built files are pushed, and there
// is no Node runtime, no SSR, no edge function and no rewrite rule at serve
// time. Every option below follows from that one fact.
export default defineConfig({
  // `site` is not set. It becomes the absolute base for canonical URLs and
  // sitemaps, and setting it to a hostname nobody has pointed DNS at yet would
  // bake a claim into the output that is not true. It belongs in the same
  // change that publishes.

  // Prerender everything. This is already Astro's default; it is written out
  // because the host cannot run the alternative, and a future integration that
  // quietly flips a route to on-demand would break the deploy rather than the
  // build.
  output: "static",

  build: {
    // Real `about.html`, not `about/index.html`. A generic static host has no
    // rewrite rules, so folder-index output leaves every URL dependent on the
    // server's directory-index behaviour — which differs between hosts and is
    // not something this project controls.
    format: "file",
  },

  // With `build.format: 'file'` there are no directory URLs to normalise, so
  // the trailing-slash question does not arise; 'never' states that rather
  // than leaving it to the default.
  trailingSlash: "never",

  vite: {
    ssr: {
      // @assayd/web-shared ships uncompiled .astro sources over the workspace
      // protocol. Without this, Vite externalises it and Node is handed a file
      // it cannot parse.
      noExternal: ["@assayd/web-shared"],
    },
  },
});
