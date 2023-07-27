# syntax=docker/dockerfile:1

FROM golang:1.20
RUN apt-get update && apt-get install -y netcat

# Set destination for COPY
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code. Note the slash at the end, as explained in
# https://docs.docker.com/engine/reference/builder/#copy
COPY *.go ./
COPY examples/envoy-plugin/ ./examples/envoy-plugin/

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /hello ./examples/envoy-plugin

# Optional:
# To bind to a TCP port, runtime parameters must be supplied to the docker command.
# But we can document in the Dockerfile what ports
# the application is going to listen on by default.
# https://docs.docker.com/engine/reference/builder/#expose
EXPOSE 8080

HEALTHCHECK \
    --interval=1s \
    --timeout=1s \
    --start-period=1s \
    --retries=3 \
    CMD nc -zv localhost 8080

# Run
ENTRYPOINT ["/hello"]
