# Sign-in provider setup

All providers are optional. Add both the client ID and secret for a provider in
`.env` locally or the production secret store. Empty values and `replace-me`
placeholders keep that provider off the sign-in page. Reddit also requires
`REDDIT_USER_AGENT` as described below. Use the same `BASE_URL`
registered with each provider; callback URLs must match exactly, including
scheme, host, and path. Restart the server after changing credentials.

| Provider | Credentials | Callback URL | Requested access |
| --- | --- | --- | --- |
| LinkedIn | `LINKEDIN_CLIENT_ID`, `LINKEDIN_CLIENT_SECRET` | `${BASE_URL}/auth/linkedin/callback` | `openid profile` |
| X | `X_CLIENT_ID`, `X_CLIENT_SECRET` | `${BASE_URL}/auth/x/callback` | `users.read tweet.read` |
| Google | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | `${BASE_URL}/auth/google/callback` | `openid profile` |
| GitHub | `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | `${BASE_URL}/auth/github/callback` | `read:user` |
| Reddit | `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET` | `${BASE_URL}/auth/reddit/callback` | `identity` |

For a local server with `BASE_URL=http://localhost:8080`, register, for
example, `http://localhost:8080/auth/google/callback`. Production needs the
production HTTPS base URL instead.

## Google

In [Google Cloud Console](https://console.cloud.google.com/apis/credentials),
configure the OAuth consent screen and create an **OAuth client ID** of type
**Web application**. Add the exact Google callback URL as an authorized redirect
URI. Enter its client ID and secret in `GOOGLE_CLIENT_ID` and
`GOOGLE_CLIENT_SECRET`. While the consent screen is in testing, add the intended
sign-in accounts as test users. Google returns an ID token; the server validates
its signature, issuer, audience and expiry before creating a session.

## GitHub

In [GitHub Developer settings](https://github.com/settings/developers), create
an **OAuth App** and set its Authorization callback URL to the exact GitHub
callback URL above. Put the app's Client ID and generated Client Secret in
`GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`. The server reads `/user` and
stores GitHub's stable numeric user ID. It does not request email access.

## Reddit

Reddit API use requires [approved access under Reddit's Responsible Builder
Policy](https://support.reddithelp.com/hc/en-us/articles/42728983564564-Responsible-Builder-Policy).
Once approved, create a Reddit **web app** with the exact Reddit callback URL
as its redirect URI. Put the app ID and secret in `REDDIT_CLIENT_ID` and
`REDDIT_CLIENT_SECRET`. The integration requests only `identity` and reads
`/api/v1/me` through Reddit's OAuth API. Token and profile requests send an
identifying `User-Agent`, as Reddit requires. Set `REDDIT_USER_AGENT` to
`web:unsolved:v1 (by /u/YOUR_REDDIT_USERNAME)`, replacing the username with your
real operator account. Quote the whole value in `.env` because it contains
spaces, for example `REDDIT_USER_AGENT='web:unsolved:v1 (by /u/your_account)'`.
Do not use the example account; use your own. Reddit stays unavailable when
this setting is missing. See [Reddit's client identification rules](https://github.com/reddit-archive/reddit/wiki/API).
Local fake-provider tests validate
the flow, but live sign-in remains unverified until Reddit issues credentials.

## Existing providers and account identity

See [LinkedIn setup](LINKEDIN-SETUP.md) for its product and consent steps.
For X, register the exact X callback above in the app's OAuth 2.0 settings.
Existing LinkedIn and X credentials keep working. Adding new provider secrets
does not rotate the signing key used by existing sessions.

Sign-in identities are keyed by provider and that provider's user ID. Signing
in through another provider creates a separate account, even when names or
email addresses match. Account linking is not supported.

Sources: [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect),
[GitHub OAuth web flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps),
[GitHub scopes](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps),
[Reddit OAuth2](https://github.com/reddit-archive/reddit/wiki/OAuth2).
