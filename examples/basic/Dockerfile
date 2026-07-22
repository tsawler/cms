# syntax=docker/dockerfile:1
# Production image for the example site. Build from the REPO ROOT so the
# whole module is in the context:
#
#	docker build -f examples/basic/Dockerfile -t cms-example .
#
# The site stylesheet (static/site.css) is generated during the build and
# the Tailwind standalone CLI ships in the runtime image for the CMS's
# content-driven rebuilds — both from the same pinned version, so the two
# stylesheets can never disagree about a class.
#
# Runtime configuration (env): DATABASE_URL (required), ADDR (default
# :4000), S3_* for media, CAP_* for the login CAPTCHA — see main.go.
# CMS_TAILWIND_COMMAND is baked in below.
#
# Kubernetes note: with readOnlyRootFilesystem, mount an emptyDir at
# /tmp — the rebuild stages its files there.

# Pin to the Tailwind version the site's CSS is authored against.
ARG TAILWIND_VERSION=v4.3.3

FROM golang:1.25-alpine AS build
ARG TAILWIND_VERSION
ARG TARGETARCH
# Standalone Tailwind CLI (no node): musl build for Alpine, which needs
# the C++ runtime the binary links against.
RUN apk add --no-cache libstdc++ \
    && case "$TARGETARCH" in \
        amd64) tw_arch=x64 ;; \
        arm64) tw_arch=arm64 ;; \
        *) echo "unsupported arch: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && wget -qO /usr/local/bin/tailwindcss \
        "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${tw_arch}-musl" \
    && chmod +x /usr/local/bin/tailwindcss

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ./examples/basic
# The site stylesheet is a build artifact (gitignored), generated here
# with the same pinned CLI the runtime image carries.
RUN cd examples/basic && tailwindcss --input assets/input.css --output static/site.css --minify

FROM alpine:3.21
# ca-certificates: HTTPS to the S3 endpoint. libstdc++: required by the
# Tailwind CLI. The rebuild path otherwise needs only busybox sh.
RUN apk add --no-cache ca-certificates libstdc++ && adduser -D -H app
COPY --from=build /usr/local/bin/tailwindcss /usr/local/bin/tailwindcss
WORKDIR /srv
COPY --from=build /out/app ./app
COPY --from=build /src/examples/basic/static ./static
COPY --from=build /src/examples/basic/tailwind-content.sh ./tailwind-content.sh
ENV CMS_TAILWIND_COMMAND="/srv/tailwind-content.sh {content} {output}"
ENV ADDR=:4000
EXPOSE 4000
USER app
ENTRYPOINT ["/srv/app"]
