import { redirect } from 'next/navigation';
import { auth, signIn } from '@/auth';

export default async function SignInPage() {
  if ((await auth())?.user?.id) redirect('/');
  return (
    <main className="signinPage">
      <section className="signinPanel">
        <div className="brandMark">ai-desktops</div>
        <h1>Control your workspace.</h1>
        <p>Sign in to manage your organization’s cloud desktops.</p>
        <form
          action={async () => {
            'use server';
            await signIn('google', { redirectTo: '/' });
          }}
        >
          <button className="googleButton" type="submit">
            <GoogleMark />
            Continue with Google
          </button>
        </form>
        <small>Access is scoped to organizations you belong to.</small>
      </section>
      <aside className="signinAside">
        <div className="signal">
          <i />
          <span>Private organization boundary</span>
        </div>
        <strong>One place for every development desktop.</strong>
        <p>Inspect readiness, control lifecycle state, and keep access aligned with your team.</p>
      </aside>
    </main>
  );
}

function GoogleMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="#4285f4"
        d="M21.6 12.23c0-.71-.06-1.4-.18-2.06H12v3.9h5.38a4.6 4.6 0 0 1-2 3.02v2.53h3.24c1.9-1.75 2.98-4.33 2.98-7.39Z"
      />
      <path
        fill="#34a853"
        d="M12 22c2.7 0 4.97-.9 6.62-2.42l-3.24-2.52c-.9.6-2.05.96-3.38.96-2.61 0-4.82-1.77-5.61-4.14H3.04v2.6A10 10 0 0 0 12 22Z"
      />
      <path
        fill="#fbbc05"
        d="M6.39 13.88a6.01 6.01 0 0 1 0-3.76v-2.6H3.04a10 10 0 0 0 0 8.96l3.35-2.6Z"
      />
      <path
        fill="#ea4335"
        d="M12 5.98c1.47 0 2.79.5 3.82 1.5l2.87-2.87A9.63 9.63 0 0 0 12 2a10 10 0 0 0-8.96 5.52l3.35 2.6C7.18 7.75 9.39 5.98 12 5.98Z"
      />
    </svg>
  );
}
