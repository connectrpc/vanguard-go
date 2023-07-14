# Envoy External Processor Example

## Set up

1.  Run the external processor

    ```sh
    go run ./cmd/extproc
    ```

1.  Run an HTTP server of some kind on port :8080

    ```sh
    python3 -m http.server 8080
    ```

1.  Install and run Envoy

    ```sh
    envoy -c examples/envoy-extproc/envoy.yaml
    ```
