import { Compass } from "lucide-react";
import { Link } from "react-router";

import { EmptyState } from "../components/ui";

export default function NotFoundPage() {
  return (
    <div className="py-10">
      <EmptyState
        icon={Compass}
        title="Page not found"
        description="The route does not exist in this SPA, or the object was removed. Use the sidebar to go back to a known page."
        action={
          <Link to="/" className="btn btn-primary">
            Back to dashboard
          </Link>
        }
      />
    </div>
  );
}
