#!/usr/bin/env bash
set -euo pipefail

OPML_FILE="$(cd "$(dirname "$0")" && pwd)/engblogs.opml"

AUTO=false

usage() {
  echo "Usage: $0 [-y] <blog-url>"
  echo "  Discovers the RSS/Atom feed for a blog and adds it to engblogs.opml"
  echo ""
  echo "Options:"
  echo "  -y   Auto-confirm: pick the first feed and accept the title without prompting"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -y) AUTO=true; shift ;;
    -*) echo "Unknown option: $1"; usage ;;
    *)  blog_url="$1"; shift ;;
  esac
done

[ -z "${blog_url:-}" ] && usage

# Ensure URL has a scheme
if [[ ! "$blog_url" =~ ^https?:// ]]; then
  blog_url="https://$blog_url"
fi

echo "Fetching $blog_url ..."
page=$(curl -sL --max-time 15 "$blog_url") || {
  echo "Error: could not fetch $blog_url"
  exit 1
}

# Discover feed URLs from <link rel="alternate" type="application/rss+xml" ...>
# and <link rel="alternate" type="application/atom+xml" ...>
# We look for href values in matching link tags.
feed_urls=()
while IFS= read -r line; do
  [ -n "$line" ] && feed_urls+=("$line")
done < <(
  echo "$page" \
    | grep -ioE '<link[^>]+(application/(rss|atom)\+xml)[^>]*>' \
    | grep -ioE 'href="[^"]*"' \
    | sed 's/^href="//i; s/"$//'
)

if [ ${#feed_urls[@]} -eq 0 ]; then
  echo "No RSS/Atom feed found in the page HTML."
  exit 1
else
  feed_url="${feed_urls[0]}"
  if [ ${#feed_urls[@]} -gt 1 ]; then
    echo "Found multiple feeds:"
    for i in "${!feed_urls[@]}"; do
      echo "  [$i] ${feed_urls[$i]}"
    done
    if [ "$AUTO" = true ]; then
      echo "Auto-selecting first feed."
    else
      read -rp "Pick a feed [0]: " pick
      pick="${pick:-0}"
      feed_url="${feed_urls[$pick]}"
    fi
  fi
fi

# Resolve relative URLs against the blog URL
if [[ ! "$feed_url" =~ ^https?:// ]]; then
  if [[ "$feed_url" =~ ^// ]]; then
    # Protocol-relative
    feed_url="${blog_url%%://*}:$feed_url"
  elif [[ "$feed_url" =~ ^/ ]]; then
    # Absolute path -- extract origin from blog_url
    origin=$(echo "$blog_url" | sed -E 's|(https?://[^/]+).*|\1|')
    feed_url="${origin}${feed_url}"
  else
    # Relative path
    base="${blog_url%/}/"
    feed_url="${base}${feed_url}"
  fi
fi

# Check for duplicate feed URL in the OPML
if grep -qF "\"$feed_url\"" "$OPML_FILE"; then
  echo "Feed already exists in engblogs.opml: $feed_url"
  exit 1
fi

echo "Feed URL: $feed_url"
echo ""
echo "Fetching feed to extract title..."
feed=$(curl -sL --max-time 15 "$feed_url") || {
  echo "Error: could not fetch feed at $feed_url"
  exit 1
}

# Extract title from the feed (works for both RSS and Atom)
title=$(echo "$feed" | sed -n 's/.*<title[^>]*>\(.*\)<\/title>.*/\1/p' | head -1)
# Strip CDATA wrappers if present
title=$(echo "$title" | sed 's/<!\[CDATA\[//g; s/\]\]>//g' | xargs)

if [ -z "$title" ]; then
  title="(unknown)"
fi

echo ""
echo "  Title:    $title"
echo "  Feed:     $feed_url"
echo "  Site:     $blog_url"
echo ""

if [ "$AUTO" = true ]; then
  echo "Auto-confirming."
else
  read -rp "Use this title? [Y/n/custom title]: " answer

  if [ -z "$answer" ] || [[ "$answer" =~ ^[Yy]$ ]]; then
    : # keep title as-is
  elif [[ "$answer" =~ ^[Nn]$ ]]; then
    read -rp "Enter custom title: " title
  else
    # Treat the answer itself as the custom title
    title="$answer"
  fi
fi

# Escape XML special characters in values
xml_escape() {
  local s="$1"
  s="${s//&/&amp;}"
  s="${s//</&lt;}"
  s="${s//>/&gt;}"
  s="${s//\"/&quot;}"
  echo "$s"
}

esc_title=$(xml_escape "$title")
esc_feed=$(xml_escape "$feed_url")
esc_html=$(xml_escape "$blog_url")

outline_line="<outline type=\"rss\" text=\"${esc_title}\" xmlUrl=\"${esc_feed}\" htmlUrl=\"${esc_html}\"/>"

echo ""
echo "Adding to engblogs.opml:"
echo "  $outline_line"
echo ""

# Insert before the closing </outline> tag (the last one in the file)
# We find the last </outline> and insert before it
sed -i '' "s|^</outline>|${outline_line}\n</outline>|" "$OPML_FILE"

echo "Done."
