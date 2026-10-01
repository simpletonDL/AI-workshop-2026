#!/bin/sh
# Installs Playwright's headless Chromium and its ffmpeg (video). If the Playwright CDN is blocked,
# takes the same Chrome for Testing build from Google's storage and ffmpeg from ffmpeg-static.
set -eu
npx playwright install chromium-headless-shell ffmpeg && exit 0
echo "Playwright CDN unavailable, using storage.googleapis.com/chrome-for-testing-public" >&2

location() { sed -n 's/^ *Install location: *//p' | head -1; }
info=$(npx playwright install --dry-run chromium-headless-shell)
dir=$(echo "$info" | location)
url=$(echo "$info" | sed -n 's#^ *Download url: *https://cdn.playwright.dev/builds/cft/#https://storage.googleapis.com/chrome-for-testing-public/#p' | head -1)
[ -n "$url" ] || { echo "no Chrome for Testing build for this platform" >&2; exit 1; }
curl -fsSL -o /tmp/chrome.zip "$url"
mkdir -p "$dir" && unzip -q /tmp/chrome.zip -d "$dir" && rm /tmp/chrome.zip
touch "$dir/INSTALLATION_COMPLETE"

dir=$(npx playwright install --dry-run ffmpeg | location)
mkdir -p "$dir" && ln -sf "$(node -p 'require("ffmpeg-static")')" "$dir/ffmpeg-linux"
touch "$dir/INSTALLATION_COMPLETE"
