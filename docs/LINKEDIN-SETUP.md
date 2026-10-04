# Set up LinkedIn sign-in

Unsolved needs credentials from your own LinkedIn developer app. Empty values
and the old `replace-me` examples leave that sign-in option unavailable.

1. Open https://www.linkedin.com/developers/apps/new and create an app named
   **Unsolved**. Supply the LinkedIn Page, logo and other details requested by
   the form. Complete any Page verification requested by LinkedIn.
2. In **Products**, request **Sign In with LinkedIn using OpenID Connect**.
   Wait until access is granted. This app requests only `openid` and `profile`.
3. In **Auth**, add the callback matching your running app. With the current
   local `BASE_URL=http://localhost:8080`, it is:

   ```text
   http://localhost:8080/auth/linkedin/callback
   ```

   LinkedIn's published guide specifies HTTPS callbacks. If the portal rejects
   the local HTTP address, use an HTTPS development address, set `BASE_URL` to
   that origin, and register that origin plus `/auth/linkedin/callback`.
   Production must use the site's HTTPS address. Start sign-in from the same
   origin so the browser can return the OAuth state cookie.
4. From **Auth**, copy the Client ID and Client Secret into the project's local
   `.env` file, replacing the existing values:

   ```dotenv
   LINKEDIN_CLIENT_ID=your-issued-client-id
   LINKEDIN_CLIENT_SECRET=your-issued-client-secret
   ```

   Keep the secret out of chat, screenshots and Git. Leave X credentials empty
   unless you also configure an X application.
5. Restart the running server to reload `.env` (`make run`, or restart
   `make dev` if using the development watcher). Open `/signin` and choose
   **Continue with LinkedIn**.

The login button checks that credentials are present, not whether LinkedIn
has approved them. A complete live login is still needed to confirm setup.

Official references:
- [LinkedIn OpenID Connect setup](https://learn.microsoft.com/en-us/linkedin/consumer/integrations/self-serve/sign-in-with-linkedin-v2)
- [OAuth credentials and redirect URLs](https://learn.microsoft.com/en-us/linkedin/shared/authentication/authorization-code-flow)
