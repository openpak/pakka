# Pakka chat: data use

Checked 2026-11-01 against Meta's published terms for the Meta Model API
(`https://api.meta.ai/v1`), which is what `internal/meta` calls.

## Model

`PAKKA_META_MODEL` defaults to `muse-spark-1.3-contributor` (`internal/meta`,
`internal/config`, `.env.example`). The id requested for the chat feature was
written as `muse-sparks-1.3-contributor`; that id does not exist in Meta's model
list and is not what is configured. Nothing was changed.

## What Meta says

- Models page, https://dev.meta.ai/docs/models.md: "In Standard mode, your content
  is not used to train Meta models." and "In Contributor mode, your content may be
  used to train future Meta models, in exchange for a lower price." `muse-spark-1.3`
  is Standard; `muse-spark-1.3-contributor` is Contributor.
- Terms of Service, https://dev.meta.ai/legal/terms-of-service:
  - 6.1: "Meta may use your Content to train, develop, evaluate, and improve Meta's
    artificial intelligence models"; before training Meta "takes steps designed to
    disassociate that Content from your Accounts and your Meta Model API key".
  - 6.2: "the Contributor Services do not offer a mechanism to exclude specific
    traffic from training." and "You must not submit sensitive, confidential, or
    personal information to the Contributor Services."
  - 10.1: "You and your End Users must be at least 18 years of age to use the
    Services and Integrated Products".
  - 4.4: Meta retains Content and Usage Data "as needed" for listed purposes. No
    fixed retention period is published.
- Data Commitments, https://dev.meta.ai/legal/commitments: "Meta may use your
  Content, including Inputs and Outputs, to train, develop, evaluate, and improve"
  for Discounted Services; "Meta will retain your Content as described in the Terms
  and Privacy Policy."
- Privacy Policy: https://www.facebook.com/privacy/policy/

Retention period: not published. Opt-out: none for the contributor tier; the only
route is switching to the Standard model id (a configuration change for the
operator, not something a player can do).

## What follows for Pakka

- Players are told their messages "may be used by Meta to train and improve its
  models" and must accept before the first message. Acceptance is stored per account
  and per notice version (`NoticeVersion` in `internal/chat/consent.go`); change the
  wording, bump the version.
- Meta requires end users to be 18 or over. Pakka chat is therefore refused for
  child, supervised, under-18 and unknown-age accounts, with no parental override.
  Pakka asks the website (which owns the account's age and supervision status)
  `eligibility` on every gated call and refuses when the answer is no or cannot be
  obtained.
- Because personal information must not be submitted, everything sent to the model is
  passed through `chat.Sanitize` (emails, friend codes, IP addresses, ids, serials,
  phone-like numbers, "password is ..." statements, bearer tokens). Players are told
  not to share passwords or personal details.

## What is sent to Meta

- The system prompt, the last 20 turns of the conversation (sanitised), and tool
  results (sanitised and infrastructure-redacted).
- Tool results are summaries: console names (not serials), the friends count (no
  names), the Rewards balance and mission progress, public service status,
  documentation excerpts, connection check outcomes.
- Never sent: account id, email, display name, session or grant tokens, friend names.

## What OpenPak stores

Conversation text per account, for 30 days after the last message, or until the
player deletes it (the file is removed at once). OpenPak does not train on it. Data
already sent to Meta follows Meta's retention.
