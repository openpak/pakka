// Package publisher is how Pakka publishes: OpenVerse posts and communities
// as the official Pakka account, News articles to the Switch News channels,
// and (blocked until keys exist) Wii U/3DS BOSS notifications.
//
// The owner authorised Pakka as the publisher on 2026-11-01. That is
// implemented as explicit, scoped and audited permissions, not a bypass:
//
//   - Every action goes through the Registry. A tool needs its Scope, and
//     each actor (the system jobs, the model, a staff member) holds a fixed
//     set of scopes. The model holds only the draft scope: whatever it
//     writes lands in the review queue.
//   - Auto-publish is limited to templated, factual posts (News mirror,
//     Splatfest results, tournament winners, featured courses). Their text
//     comes from a template and checked facts, never from free text. That
//     rule is in code: a category's mode cannot be changed by configuration,
//     only switched on or off.
//   - Anything with free text (a model-written highlight, an announcement, a
//     new community, a News article) waits in the queue until a staff member
//     approves it (admin endpoint or the Discord staff command).
//   - PAKKA_PUBLISH_KILL stops every publish. PAKKA_PUBLISH_CATEGORIES lists
//     the categories that are on; an unlisted category refuses everything.
//   - Every attempt, refusal, approval and publish is written to the audit
//     log before it happens, and publishing is refused when the log cannot
//     be written.
package publisher
