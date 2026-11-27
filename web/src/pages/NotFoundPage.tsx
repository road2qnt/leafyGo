import { Link } from "react-router";

export default function NotFoundPage() {
  return (
    <div className="max-w-[40rem] space-y-4">
      <p className="m-0 inline-block bg-ink px-3 py-1 font-mono text-sm font-medium text-paper">
        404
      </p>
      <h1 className="m-0 text-[clamp(1.625rem,1.3rem+1.4vw,2.25rem)] font-semibold text-ink">
        Page not found
      </h1>
      <p className="m-0 text-base leading-relaxed text-ink-soft">
        The route does not exist in this SPA, or the object was removed. Use the top header to
        return to a known page.
      </p>
      <div className="pt-2">
        <Link to="/" className="btn btn-primary">
          Back to dashboard
        </Link>
      </div>
    </div>
  );
}
