// Package web is the server-rendered Go-template + htmx driver adapter for
// the same use cases internal/adapter/driver/http exposes as JSON — a
// second, HTML-rendering front end, not a second copy of the use cases.
//
// htmx fragment handlers (the "*_rows"/"*_results" endpoints the page's
// Refresh/submit buttons hx-get or hx-post) always respond 200, even on a
// use-case error: htmx does not swap a non-2xx response into its target by
// default, so a 404/500 response from a fragment endpoint would leave that
// target empty with the error invisible to the user. These handlers render
// the error message and slug inline inside the fragment instead, at 200.
// Full-page handlers (the "*_page" endpoints reached by navigation) have no
// such constraint and go through the normal renderError path (404 for
// domain.ErrNotFound, 500 otherwise).
package web
