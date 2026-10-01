# OAuth 2.0 & OIDC Adapter

`oauthx` provides authentication and token lifecycle management for Google OAuth 2.0 and OpenID Connect.
It implements Proof Key for Code Exchange (PKCE with S256) per the OAuth 2.1 specification.

## Public contract

- `IdentityProvider`: primary interface for OAuth 2.0 providers.
- `Google`: concrete provider implementing Google authorization, token exchange, refresh, revocation, and OIDC claims validation.
- `NewAdapter`: wraps an identity provider into a managed `adapter.Provider`.

## Features

1. **PKCE Mandatory**: Every authorization URL generates an ephemeral, cryptographically random verifier and S256 challenge.
2. **Transparent Refresh**: Exchanging refresh tokens handles token rotation detection.
3. **Session Revocation**: Provides clean token invalidation on user sign-out.
4. **OIDC Validation**: Cryptographically validates claims (`sub`, `iss`, `aud`, `exp`).
5. **Correlation Tracking**: Injects `X-Correlation-ID` on all outbound HTTP token requests.

## Security

1. Client secrets and bearer tokens are never logged.
2. Redirect targets enforce exact matching against configured redirect URLs.
3. Constant-time comparisons are used where applicable.
