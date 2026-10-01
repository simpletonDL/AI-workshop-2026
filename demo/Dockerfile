# Demo recorder: Node, headless Chromium, ffmpeg and git, so recording needs only Docker on the host
# (scripts/demo-record.sh). Images come from Docker Hub and packages from the Ubuntu archive:
# some hosts block mcr.microsoft.com and the Playwright CDN.
FROM node:24-bookworm-slim AS node

FROM ubuntu:24.04
COPY --from=node /usr/local/bin/node /usr/local/bin/
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
WORKDIR /opt/demo
COPY package.json package-lock.json install-browser.sh ./
RUN npm ci --no-audit --no-fund \
 && npx playwright install-deps chromium-headless-shell \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip git \
 && ./install-browser.sh \
 && rm -rf /var/lib/apt/lists/*
