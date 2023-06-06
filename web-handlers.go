package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

func GenerateNonce() (string, error) {
	// Define the desired length of the nonce (in bytes)
	nonceLength := 16

	// Generate random bytes for the nonce
	nonceBytes := make([]byte, nonceLength)
	_, err := rand.Read(nonceBytes)
	if err != nil {
		return "", err
	}

	// Encode the random bytes as a base64 string
	nonce := base64.StdEncoding.EncodeToString(nonceBytes)

	return nonce, nil
}

func doTimeout(w http.ResponseWriter, r *http.Request) {
	thereWasATimeout = true
	// Set the Content-Type header to "application/json"
	w.Header().Set("Content-Type", "text/html")
	http.Redirect(w, r, "/timeout", http.StatusFound)
}

func cssHandler(w http.ResponseWriter, r *http.Request) {
	filePath := "static/css/" + strings.TrimPrefix(r.URL.Path, "/static/css/")
	http.ServeFile(w, r, filePath)

	// Set the Content-Type header to "text/css"
	w.Header().Set("Content-Type", "text/css")
}

func dataHandler(w http.ResponseWriter, r *http.Request) {
	filePath := "static/data/" + strings.TrimPrefix(r.URL.Path, "/static/data/")
	http.ServeFile(w, r, filePath)

	// Set the Content-Type header to "application/json"
	w.Header().Set("Content-Type", "application/json")
}

func followRedirects(urlStr string, w http.ResponseWriter, r *http.Request) (string, []Hop, error) {
	// CF didn't break anything yet.
	cloudflareStatus = false

	client := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 5 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Stop following redirects after the first hop
			if len(via) >= 1 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	hops := []Hop{}
	number := 1

	var previousURL *url.URL

	for {
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			return "", nil, fmt.Errorf("error creating request: %s", err)
		}

		// Set the user agent header
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

		resp, err := client.Do(req)
		if err != nil {
			if err, ok := err.(*url.Error); ok && err.Timeout() {
				doTimeout(w, r)
				return "", nil, nil
			}
			return "", nil, fmt.Errorf("error accessing URL: %s", err)
		}
		defer resp.Body.Close()

		hop := Hop{
			Number:     number,
			URL:        urlStr,
			StatusCode: resp.StatusCode,
		}
		hop.StatusCodeClass = getStatusCodeClass(resp.StatusCode)
		hops = append(hops, hop)

		if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
			location := resp.Header.Get("Location")
			if location == "" {
				if strings.Contains(resp.Header.Get("Server"), "cloudflare") {
					cloudflareStatus = true
				}
				return "", []Hop{}, nil // Return empty slice of Hop when redirect location is not found
			}

			redirectURL, err := handleRelativeRedirect(previousURL, location)
			if err != nil {
				return "", nil, fmt.Errorf("error handling relative redirect: %s", err)
			}

			// Check if the "returnUri" query parameter is present
			u, err := url.Parse(redirectURL)
			if err != nil {
				return "", nil, fmt.Errorf("error parsing URL: %s", err)
			}
			queryParams := u.Query()
			if returnURI := queryParams.Get("returnUri"); returnURI != "" {
				decodedReturnURI, err := url.PathUnescape(returnURI)
				if err != nil {
					return "", nil, fmt.Errorf("error decoding returnUri: %s", err)
				}
				decodedReturnURI = strings.ReplaceAll(decodedReturnURI, "%3A", ":")
				decodedReturnURI = strings.ReplaceAll(decodedReturnURI, "%2F", "/")

				redirectURL = u.Scheme + "://" + u.Host + u.Path + "?returnUri=" + decodedReturnURI
			}

			urlStr = redirectURL
			number++

			previousURL, err = url.Parse(urlStr)
			if err != nil {
				return "", nil, fmt.Errorf("error parsing URL: %s", err)
			}
			continue
		}

		return urlStr, hops, nil
	}
}

func getStatusCodeClass(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "2xx"
	case statusCode >= 300 && statusCode < 400:
		return "3xx"
	case statusCode >= 400 && statusCode < 500:
		return "4xx"
	case statusCode >= 500 && statusCode < 600:
		return "5xx"
	default:
		return ""
	}
}

func handleRelativeRedirect(previousURL *url.URL, location string) (string, error) {
	redirectURL, err := url.Parse(location)
	if err != nil {
		return "", err
	}

	// Check if the redirect URL is an absolute URL
	if !redirectURL.IsAbs() {
		// Use the domain from the previous URL
		if previousURL != nil {
			redirectURL.Scheme = previousURL.Scheme
			redirectURL.Host = previousURL.Host
		} else {
			// Use the current host
			currentURL, err := url.Parse(location)
			if err == nil {
				redirectURL.Scheme = currentURL.Scheme
				redirectURL.Host = currentURL.Host
			} else {
				return "", err
			}
		}
	}
	absoluteURL := redirectURL.String()
	return absoluteURL, nil
}

func homeHandler(w http.ResponseWriter, r *http.Request, config *Config) {
	nonce, err := GenerateNonce()
	if err != nil {
		fmt.Println("Failed to generate nonce:", err)
	}

	// Set security headers
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; script-src 'nonce-%s'", nonce))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	if r.Method == "GET" {
		data := struct {
			Nonce    string
			UseCount int
		}{
			Nonce:    nonce, // Pass the nonce value to the template data
			UseCount: config.UseCount,
		}
		formTemplate.Execute(w, data)
	} else {
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func jsHandler(w http.ResponseWriter, r *http.Request) {
	filePath := "static/js/" + strings.TrimPrefix(r.URL.Path, "/static/js/")
	http.ServeFile(w, r, filePath)

	// Set the Content-Type header to "application/javascript"
	w.Header().Set("Content-Type", "application/javascript")
}

func traceHandler(w http.ResponseWriter, r *http.Request, config *Config) {
	// No timeouts, yet.
	thereWasATimeout = false

	// Increment the UseCount
	config.UseCount++
	fmt.Println("Updated UseCount:", config.UseCount)

	nonce, err := GenerateNonce()
	if err != nil {
		fmt.Println("Failed to generate nonce:", err)
	}

	// Set security headers
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; script-src 'nonce-%s'", nonce))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	var rawURL string
	if r.Method == "POST" {
		rawURL = r.FormValue("url")
	} else if r.Method == "GET" {
		token := r.URL.Query().Get("token")
		if token != "" && token == os.Getenv("GET_TOKEN") {
			rawURL = r.URL.Query().Get("url")
		}
	} else {
		http.Redirect(w, r, "/", http.StatusFound)
	}

	// Validate the URL format
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || !parsedURL.IsAbs() {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}

	// Check if the URL parameter contains the server name
	if strings.Contains(parsedURL.Host, r.Host) {
		http.Error(w, "Redirecting to URLs within the same server is not allowed", http.StatusBadRequest)
		return
	}

	// Sanitize URL input using bluemonday
	sanitizedURL := bluemonday.UGCPolicy().Sanitize(rawURL)
	fixedSanitizedURL := strings.ReplaceAll(sanitizedURL, "&amp;", "&")

	redirectURL, hops, err := followRedirects(fixedSanitizedURL, w, r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error following redirects: %s", err), http.StatusInternalServerError)
		return
	}

	lastIndex := len(hops) - 1

	var finalStatusCode int
	var finalMessage string

	if lastIndex >= 0 {
		finalStatusCode = hops[lastIndex].StatusCode
	} else {
		finalStatusCode = http.StatusInternalServerError
		finalMessage = "Redirect Location Not Provided By Headers"
		hops = append(hops, Hop{Number: 1, URL: rawURL, StatusCode: finalStatusCode, StatusCodeClass: getStatusCodeClass(finalStatusCode)})
	}

	data := ResultData{
		RedirectURL:      redirectURL,
		Hops:             hops,
		LastIndex:        lastIndex,
		StatusCode:       finalStatusCode,
		FinalMessage:     template.HTML(finalMessage),
		Nonce:            nonce,
		CloudflareStatus: cloudflareStatus,
	}

	if !thereWasATimeout {
		resultTemplate.Execute(w, data)
	}
}