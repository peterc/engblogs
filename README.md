# Engineering Blogs

Source for [engineeringblogs.xyz](https://engineeringblogs.xyz/) -- a single-page aggregator of engineering blog posts from the past seven days.

The site is rebuilt every 4 hours by GitHub Actions and deployed to GitHub Pages. No database, no JS, no external services -- just a Go script that fetches RSS/Atom feeds and renders a static HTML page.

## Requirements

- Go 1.22+
- Python 3 (for the local dev server)

## Usage

`make build` -- Fetches all feeds from `engblogs.opml`, collects posts from the last 7 days, and generates `public/index.html`. A `cache.json` file stores ETags for conditional GET on subsequent runs.

`make render` -- Rebuilds HTML from cache without fetching feeds. Fast, useful for tweaking the template.

`make dev` -- Renders then serves `public/` at http://localhost:8080.

`make clean` -- Removes the `public/` directory and `cache.json`.

## Adding a feed

[Open an issue](https://github.com/peterc/engblogs/issues/new?template=add-feed.yml) with the blog name and feed URL, or edit `engblogs.opml` directly and open a PR. New feeds show up on the site within minutes of merging.
