package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CobaltService interacts with the Cobalt API.
type CobaltService struct {
	ApiUrl string
	Url    string
}

// CobaltResponse represents the response from the Cobalt API.
type CobaltResponse struct {
	Status   string `json:"status"` // redirect, tunnel, local-processing, picker, error
	Url      string `json:"url"`
	Filename string `json:"filename"`
	Picker   []struct {
		Type  string `json:"type"` // photo / video / gif
		Url   string `json:"url"`
		Thumb string `json:"thumb"` // thumbnail url (optional)
	} `json:"picker"`
}

// CobaltRequestBody represents the request body for the Cobalt API.
type CobaltRequestBody struct {
	Url string `json:"url"`
}

// Download sends a request to the Cobalt API to download a file.
func (c *CobaltService) Download() ([]string, error) {
	body := CobaltRequestBody{
		Url: c.Url,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		fmt.Println("[Cobalt] failed to marshal request body", err)
		return []string{}, fmt.Errorf("[Cobalt] failed to marshal request body: %w", err)
	}
	fmt.Println("[Cobalt] sending request to", c.ApiUrl)

	request, err := http.NewRequest("POST", c.ApiUrl, bytes.NewReader(jsonBody))
	if err != nil {
		fmt.Println("[Cobalt] failed to create request", err)
		return []string{}, fmt.Errorf("[Cobalt] failed to create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		fmt.Println("[Cobalt] failed to send request", err)
		return []string{}, fmt.Errorf("[Cobalt] failed to send request: %w", err)
	}
	defer response.Body.Close()

	var cobaltResponse CobaltResponse

	fmt.Printf("[Cobalt] response status code: %d\n", response.StatusCode)
	if response.StatusCode != http.StatusOK {
		fmt.Println("[Cobalt] received non-200 response")
		// print response body
		body, _ := io.ReadAll(response.Body)
		fmt.Println("[Cobalt] response body:", string(body))
		return []string{}, fmt.Errorf("[Cobalt] received non-200 response: %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&cobaltResponse); err != nil {
		fmt.Println("[Cobalt] failed to decode response", err)
		return []string{}, fmt.Errorf("[Cobalt] failed to decode response: %w", err)
	}
	switch cobaltResponse.Status {
	case "error":
		return []string{}, fmt.Errorf("cobalt service error: %s", cobaltResponse.Url)
	case "local-processing":
		return []string{}, fmt.Errorf("cobalt service is processing the file locally: %s", cobaltResponse.Url)
	case "redirect":
		return []string{cobaltResponse.Url}, nil
	case "tunnel":
		return []string{cobaltResponse.Url}, nil
	case "picker":
		if len(cobaltResponse.Picker) > 0 {
			var urls []string
			for _, p := range cobaltResponse.Picker {
				urls = append(urls, p.Url)
			}
			return urls, nil
		}
	}

	return []string{}, fmt.Errorf("unexpected cobalt response status: %s", cobaltResponse.Status)
}
