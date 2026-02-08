# engineeringblogs.xyz

A single-page aggregator of engineering blog posts covering the past seven days only. There is an OPML file with all the feeds.

## What the site does

Displays recent posts from hundreds of engineering blogs on a single static HTML page. Updated automatically every few hours via GitHub Actions and served by GitHub Pages.

Look at https://engineeringblogs.xyz/ for the current basic layout. I want something similar just as a step 1 but we will make it fancier later.

We need to be able to run it locally in dev too.

## Stack

- **Go** for the build script
- **GitHub Actions** for scheduled builds
- **GitHub Pages** for hosting
- No database, no AWS, no external services, we can save the latest posts into a JSON file and cache it between builds. We need to do this because we're going to use conditional GET, so some feeds will not be fetched on every run.

## Build script

The build script can pretty much do everything both in dev and prod:

1. **Parse OPML**. Extract title, xmlUrl, htmlUrl for each feed.
2. **Deduplicate** feeds by xmlUrl. Also deduplicate collected entries by post URL so cross-syndicated posts only appear once.
3. **Fetch all feeds in parallel.** ~30 concurrent connections. Per-feed timeout of 10 seconds. Log failures to stderr but keep going. Use conditional get, we can store the etags in a cached (but not committed to the repo) json file using github's build cache thing.
4. **Collect entries** from the last 7 days. Normalize dates to UTC.
5. **Sort entries** by publish date, newest first.
6. **Render HTML** into `public/index.html` (gitignored). GitHub Pages deploys from this directory. The OPML file is also copied into `public/` so it's served alongside the page.

Exit 0 even if some feeds fail (they always will). Print a summary to stderr: total feeds, successful fetches, failed fetches, total entries.

### Feed fetching details

- Set a proper User-Agent: `"EngBlogs/1.0 (+https://engineeringblogs.xyz)"`.
- 10 second connect timeout, 15 second read timeout per feed.
- Follow redirects.
- Catch and log all errors per-feed: timeouts, SSL errors, parse failures, HTTP errors.
- Don't retry failures. If a feed is down this cycle, it'll get picked up next time.

## Template

A Go `html/template` rendering a clean, minimal, single-page HTML file. Self-contained (no external CSS/JS dependencies).

Key elements:
- `<meta charset="utf-8">` and viewport meta tag.
- Page title with feed count and last-built timestamp.
- Link to the OPML file (served from the repo or GitHub raw URL).
- Entries grouped by date, each showing: blog name, post title (linked), and time.
- Responsive: single-column on mobile, two-column (source + title) on desktop.
- A footer linking to the GitHub repo (peterc/engblogs) and inviting submissions.
- Light, fast, no JavaScript required for core functionality.

Keep the current design direction (monospace-influenced, minimal, blue accent) or improve on it -- but don't over-design it. It's a feed list.

## OPML file management

- The OPML stays in the repo root, edited by hand or via PRs.
- On push to main, the build runs, so new feeds show up on the site within minutes.
- No separate upload step needed. The build script reads the OPML from disk, not from S3.
- Keep the issue template for community feed suggestions.

## Local development

A `Makefile` provides the local dev workflow:

- `make build` — runs the Go build script, outputs to `public/`.
- `make dev` — runs the build, then serves `public/` on a local port with a simple Go file server (or `python3 -m http.server`) and opens the browser.
- `make clean` — removes `public/` and the cache JSON.

## What's NOT included (intentionally)

- No database. Feed entries are ephemeral -- recrawled every build.
- No JavaScript on the page. It's static HTML.
- No feed health monitoring dashboard. Failed feeds are logged in the Actions run.
- No full-text content. Just titles and links, same as now.
