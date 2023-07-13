ARG ENVOY_IMAGE="${ENVOY_IMAGE:-envoyproxy/envoy}"
ARG ENVOY_VARIANT="${ENVOY_VARIANT:-dev}"

FROM ${ENVOY_IMAGE}:${ENVOY_VARIANT}
ARG ENVOY_CONFIG=envoy.yaml
ENV ENVOY_CONFIG="$ENVOY_CONFIG"
ENV DEBIAN_FRONTEND=noninteractive
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' | tee /etc/apt/apt.conf.d/keep-cache \
    && apt-get -qq update -y \
    && apt-get -qq install --no-install-recommends -y curl
COPY --chmod=777 "$ENVOY_CONFIG" /etc/envoy.yaml

ENV GODEBUG=cgocheck=0
COPY --chmod=777 ./lib/vanguard.so /lib/vanguard.so

CMD ["/usr/local/bin/envoy", "-c", "/etc/envoy.yaml"]
