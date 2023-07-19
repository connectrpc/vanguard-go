# syntax=docker/dockerfile:1

FROM debian:bullseye-slim@sha256:9404b05bd09b57c76eccc0c5505b3c88b5feccac808d9b193a4fbac87bb44745 as os-base
RUN rm -f /etc/apt/apt.conf.d/docker-clean \
    && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' | tee /etc/apt/apt.conf.d/keep-cache


FROM golang:1.20.3-bullseye@sha256:595c9af0430dd84bad33020e7e9e328af4bd1a1aabd46a03b5bf6f252cdbecf3 as golang-base
