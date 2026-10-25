# Build backend binary file
FROM golang:1.27.1-alpine3.24 AS be-builder
ARG RELEASE_BUILD
ARG BUILD_PIPELINE
ARG BUILD_UNIXTIME
ARG BUILD_DATE
ARG CHECK_3RD_API
ARG SKIP_TESTS
ENV RELEASE_BUILD=$RELEASE_BUILD
ENV BUILD_PIPELINE=$BUILD_PIPELINE
ENV BUILD_UNIXTIME=$BUILD_UNIXTIME
ENV BUILD_DATE=$BUILD_DATE
ENV CHECK_3RD_API=$CHECK_3RD_API
ENV SKIP_TESTS=$SKIP_TESTS
WORKDIR /go/src/github.com/mayswind/ezbookkeeping
COPY . .
RUN docker/backend-build-pre-setup.sh
RUN apk add git gcc g++ libc-dev
# Tofu (2026-10-25): --no-lint --no-test. Upstream's CI runs go vet and the unit tests (after `go clean -cache`) on
# this commit. In a build without BUILD_PIPELINE=1, as on Tofu, the exchange-rate tests also call the live API of
# every central bank ezBookkeeping supports, so one slow or unreachable bank would fail a copy's build.
RUN ./build.sh backend --no-lint --no-test

# Build frontend files
FROM --platform=$BUILDPLATFORM node:26.10.0-alpine3.24 AS fe-builder
ARG RELEASE_BUILD
ARG BUILD_PIPELINE
ARG BUILD_UNIXTIME
ARG BUILD_DATE
ARG BUILD_NODE_OPTIONS
ENV RELEASE_BUILD=$RELEASE_BUILD
ENV BUILD_PIPELINE=$BUILD_PIPELINE
ENV BUILD_UNIXTIME=$BUILD_UNIXTIME
ENV BUILD_DATE=$BUILD_DATE
ENV NODE_OPTIONS=$BUILD_NODE_OPTIONS
WORKDIR /go/src/github.com/mayswind/ezbookkeeping
COPY . .
RUN docker/frontend-build-pre-setup.sh
RUN apk add git
# Tofu (2026-10-25): --no-lint --no-test, as for the backend above (vue-tsc, eslint and vitest).
RUN ./build.sh frontend --no-lint --no-test

# Package docker image
FROM alpine:3.24.2
LABEL maintainer="MaysWind <i@mayswind.net>"
RUN addgroup -S -g 1000 ezbookkeeping && adduser -S -G ezbookkeeping -u 1000 ezbookkeeping
RUN apk --no-cache add tzdata
COPY docker/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
# Tofu (2026-10-25): Tofu's own entrypoint maps PORT and DATABASE_URL onto ezBookkeeping's settings, then runs the
# upstream entrypoint above (see TOFU.md).
COPY tofu-entrypoint.sh /tofu-entrypoint.sh
RUN mkdir -p /ezbookkeeping && chown 1000:1000 /ezbookkeeping \
  && mkdir -p /ezbookkeeping/data && chown 1000:1000 /ezbookkeeping/data \
  && mkdir -p /ezbookkeeping/log && chown 1000:1000 /ezbookkeeping/log \
  && mkdir -p /ezbookkeeping/storage && chown 1000:1000 /ezbookkeeping/storage
WORKDIR /ezbookkeeping
COPY --from=be-builder --chown=1000:1000 /go/src/github.com/mayswind/ezbookkeeping/ezbookkeeping /ezbookkeeping/ezbookkeeping
COPY --from=fe-builder --chown=1000:1000 /go/src/github.com/mayswind/ezbookkeeping/dist /ezbookkeeping/public
COPY --chown=1000:1000 conf /ezbookkeeping/conf
COPY --chown=1000:1000 templates /ezbookkeeping/templates
COPY --chown=1000:1000 LICENSE /ezbookkeeping/LICENSE
USER 1000:1000
EXPOSE 8080
# Tofu (2026-10-25): upstream starts "/docker-entrypoint.sh" directly.
ENTRYPOINT ["/bin/sh", "/tofu-entrypoint.sh"]
