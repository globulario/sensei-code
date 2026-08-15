# Provider authentication

Sensei Code exposes one login surface while keeping credential ownership with each provider's native client.

## Law

**Sensei Code does not store provider OAuth tokens, API keys, refresh tokens, passwords, or subscription credentials.**

It may keep non-secret readiness metadata such as provider ID, installed state, authentication state when the provider exposes it, account label, plan label, and provider session/thread identifiers.

A provider is not considered connected merely because its executable exists. An authentication state that cannot be verified is represented as unknown, not PASS.

## User experience

From the CLI:

```text
sensei-code providers
sensei-code login
sensei-code login chatgpt
sensei-code login codex
sensei-code login claude
sensei-code login antigravity
sensei-code logout <provider>
```

From the interactive TUI:

```text
/login

  1. ChatGPT subscription (Codex app-server)
  2. Codex native login
  3. Claude Code
  4. Google Antigravity
```

The TUI temporarily yields the terminal to the login process and resumes the same Sensei Code session when authentication finishes.

## OpenAI / ChatGPT

Sensei Code talks to the supported `codex app-server` JSON-RPC account surface. ChatGPT browser login is started with `account/login/start`; Codex owns the callback, token persistence, refresh, and logout. Sensei Code reads only the account/auth mode and optional plan/email metadata returned by `account/read`.

A Codex account authenticated by API key is valid for a Codex provider role but is not represented as a ChatGPT subscription login. Those are distinct facts.

## Claude Code

Sensei Code delegates to Claude Code's native authentication commands and reads `claude auth status` when available. Claude owns its credential storage. Sensei Code never reads Claude credential files or keychain entries.

## Google Antigravity

Sensei Code launches the native `agy` client. Google Sign-In and credential persistence remain owned by Antigravity and the operating-system keyring. Until Antigravity exposes a stable machine-readable authentication-status contract, Sensei Code represents its installed state separately from its unknown authentication state.

## Execution sessions

Authentication and execution are separate contracts. This slice establishes login/readiness. Provider execution may later move to long-lived native clients where the provider exposes a stable session protocol. In particular, Codex app-server is the preferred future OpenAI execution surface because it also owns thread/session lifecycle.

No provider session may acquire Sensei architectural, admission, proof, or completion authority merely because it is authenticated.
