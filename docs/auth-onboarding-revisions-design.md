# Sign-in, contributor onboarding and problem refinement

Approved and implemented — 2026-10-04. Live provider activation still requires
the operator's registered application credentials; see AUTH-SETUP.md.

## Intended outcome

People can join through Google, GitHub or Reddit as well as LinkedIn and X,
identify themselves as problem identifiers, problem solvers or both, and easily
correct incomplete or inaccurate problems. Revisions remain attributable and
reviewable, with the community-supported version displayed as current.

Assumption: “top of the book” means the best supported version leads the problem
page, and useful problems rise on the existing discovery board. Votes express
community support; they are not proof of factual accuracy.

## Recommended approach

Extend the existing Go authentication and revision services. Preserve the
current session, CSRF, safe-return-path and moderation rules. A hosted identity
service would introduce another vendor and account migration; rebuilding
revisions would duplicate an existing, tested subsystem.

## Sign-in

- Add Google through OpenID Connect and GitHub and Reddit through their
  server-side authorization-code flows. Request only identity/profile access.
- Extend provider storage through an additive migration and update identity
  labels, profile rendering, privacy/export handling and tests consistently.
- Only offer providers with both real configuration values present. Existing
  placeholder handling remains in force. Include setup documentation for each
  provider with exact callback paths.
- Validate OAuth state and provider identity; use PKCE where supported. Google
  ID tokens must pass issuer, audience, expiry and signature validation.
- Never merge accounts by matching names or email. Provider identities remain
  distinct unless a separately designed, authenticated account-linking flow is
  implemented. Linking is outside this change.
- Reddit integration can be implemented and tested with local fakes, but live
  activation requires approved Reddit API access and issued credentials.
- Existing LinkedIn and X users and sessions must continue to work.

## Onboarding

- After first sign-in, show a short, responsive welcome step: “How would you
  like to contribute?” Choices: “Identify problems”, “Solve problems”, “Both”.
- Explain that identifiers describe real difficulties and improve problem
  statements; solvers propose approaches and add evidence. Both can refine,
  vote and contribute across the platform; these choices do not grant powers.
- Persist the preference and completion state; allow changes in Settings.
  Existing accounts receive the same optional setup without losing access.
- Provide “Skip for now”. Preserve the original local destination, including
  a correction form, across onboarding. Without a destination, identifiers
  receive a posting CTA and solvers an open-problems CTA.
- Include preferences in account export and deletion behavior.

## Refine or correct

- Add a clearly labelled “Refine or correct” action to both the full problem
  page and explorer preview, usable on narrow and wide screens.
- Signed-out visitors see the action and sign in before reaching the prefilled
  form. Suspended users cannot submit; invalid problems remain read-only under
  the current moderation rules.
- Reuse immutable revisions: title, process, pain and attempted approaches are
  prefilled. Require a short explanation of what changed and why.
- Make “Revision history” visible beside the action. Show current-version
  status, contributor, explanation and votes. Add a readable comparison with
  the parent revision so readers can evaluate a correction.
- A submitted revision does not silently overwrite the current statement.
  Retain the existing rule: a challenger must outscore the incumbent and meet
  the configured minimum votes (default three); ties keep the incumbent.
- Explain that rule in the revision experience. Preserve existing board
  scoring and show community support honestly, without an invented AI quality
  score. A separate factual-review or ranking model would need its own design.

## Delivery and verification

Implement provider support first, onboarding second, revision discoverability
and comparison third. Use additive migrations and regenerate SQL/template
outputs. Test configured and unavailable providers, successful and rejected
callbacks, identity separation, onboarding persistence/skip/return paths,
anonymous correction redirects, write restrictions, revision comparison and
leader selection. Run existing authentication and revision regression tests,
the full Go test suite and static checks. Check desktop and mobile layouts,
keyboard access and error states. Report live provider verification separately
from fake-provider tests; credentials and provider approval are external steps.

## References

- https://developers.google.com/identity/openid-connect/openid-connect
- https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- https://support.reddithelp.com/hc/en-us/articles/42728983564564-Responsible-Builder-Policy
