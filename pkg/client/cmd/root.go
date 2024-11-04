package cmd

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/jackyes/underpass/pkg/client/tunnel"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/spf13/cobra"
)

var host string
var insecure bool
var subdomain string
var address string
var authToken string
var maxRetries int
var reconnectDelay time.Duration

var rootCmd = &cobra.Command{
	Use:   "underpass",
	Short: "The Underpass CLI",
	Run: func(cmd *cobra.Command, args []string) {
		scheme := "wss"
		if insecure {
			scheme = "ws"
		}

		// Generate random subdomain if not specified
		if subdomain == "" {
			var err error
			subdomain, err = gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 8)
			if err != nil {
				fmt.Println("Error generating subdomain:", err)
				os.Exit(1)
			}
		}

		query := url.Values{}
		query.Set("subdomain", subdomain)

		// Strip any protocol prefix from host
		cleanHost := host
		if prefix := "https://"; len(host) > len(prefix) && host[:len(prefix)] == prefix {
			cleanHost = host[len(prefix):]
		} else if prefix := "http://"; len(host) > len(prefix) && host[:len(prefix)] == prefix {
			cleanHost = host[len(prefix):]
		}

		u := url.URL{
			Scheme:   scheme,
			Path:     "start",
			Host:     cleanHost,
			RawQuery: query.Encode(),
		}

		t, err := tunnel.Connect(u.String(), address, subdomain, authToken)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
			return
		}

		// Clean display host from any protocol prefix
		displayHost := host
		if prefix := "https://"; len(host) > len(prefix) && host[:len(prefix)] == prefix {
			displayHost = host[len(prefix):]
		} else if prefix := "http://"; len(host) > len(prefix) && host[:len(prefix)] == prefix {
			displayHost = host[len(prefix):]
		}

		fmt.Print("Started tunnel: ")
		if insecure {
			color.New(color.Bold, color.FgGreen).Printf("http://%s.%s", t.Subdomain, displayHost)
		} else {
			color.New(color.Bold, color.FgGreen).Printf("https://%s.%s", t.Subdomain, displayHost)
		}
		color.New(color.FgHiBlack).Print(" --> ")
		color.New(color.Bold, color.FgCyan).Printf("%s\n\n", address)

		if err = t.Wait(); err != nil {
			fmt.Printf("\n❌ Disconnected from server. %s\n", color.New(color.FgHiBlack).Sprint(err))
			os.Exit(1)
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&host, "host", "underpass.clb.li", "Host to connect to")
	rootCmd.Flags().BoolVar(&insecure, "insecure", false, "[ADVANCED] don't tunnel over TLS")
	rootCmd.Flags().StringVarP(&subdomain, "subdomain", "s", "", "Request a custom subdomain")
	rootCmd.Flags().StringVar(&address, "address", "http://localhost:8080", "Address to forward requests to")
	rootCmd.Flags().StringVarP(&authToken, "token", "t", "", "Authentication token")
	rootCmd.Flags().IntVar(&maxRetries, "max-retries", 5, "Maximum number of reconnection attempts")
	rootCmd.Flags().DurationVar(&reconnectDelay, "reconnect-delay", 5*time.Second, "Delay between reconnection attempts")

	rootCmd.Flags().MarkHidden("insecure")
}
