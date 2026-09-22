import { Link } from "react-router";

export function Contact() {
  return (
    <main id="main" className="public-page compact-page">
      <p className="eyebrow">CONTACT US</p>
      <h1>
        Let’s talk about
        <br />
        <em>your next close.</em>
      </h1>
      <p className="hero-copy">
        Every firm has its own methods. We’d like to understand where
        preparation takes your time, what makes review difficult, and what a
        better close could look like.
      </p>
      <section>
        <h2>Our contact channel is coming soon.</h2>
        <p>
          We’re getting ready to hear from accounting firms. Contact details
          will appear here when they’re available.
        </p>
        <p>
          In the meantime, explore the illustrative workspace. No client data or
          account is needed.
        </p>
        <Link className="button primary" to="/dashboard">
          Explore the example workspace →
        </Link>
      </section>
    </main>
  );
}
