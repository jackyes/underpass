# Underpass

Self-hosted [ngrok](https://ngrok.com) alternative.

## Installation

....

## Self-hosting

(more docs coming soon, possibly)
Server:
```bash
go run server.go -h

  -CertCrt string
        Path to CertCrt
  -CertKey string
        Path to CertKey
  -TLS
        Enable TLS (need -CertCrt <path> and -CertKey <path>
  -host string
        Host address
  -port string
        Local server port (default "80")
  -token string
        Authentication token for clients

```
Client:
```bash
go run client.go -h
The Underpass CLI

Usage:
  underpass [flags]

Flags:
  -h, --help               help for underpass
      --host string        Host to connect to (default "underpass.clb.li")
  -p, --port int           Port to tunnel to
  -s, --subdomain string   Request a custom subdomain
  -t, --token string       Authentication token


underpass -p <port> --host <host here> -s <optional subdomain> -t <your_secure_key>
```


