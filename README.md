# Engineering Blogs

Source for [engineeringblogs.xyz](https://engineeringblogs.xyz/) -- a single-page aggregator of engineering blog posts from the past seven days.

## Requirements

- Go 1.21+
- Python 3 (for the local dev server)

## Usage

`make build` -- Fetches all feeds from `engblogs.opml`, collects posts from the last 7 days, and generates `public/index.html`. A `cache.json` file is written alongside the binary to store ETags for conditional GET on subsequent runs.

`make dev` -- Runs the build, then serves `public/` at http://localhost:8080.

`make clean` -- Removes the `public/` directory and `cache.json`.

## Adding a feed

Edit `engblogs.opml` directly or open a PR. The build picks up changes on the next run.
