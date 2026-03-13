package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validIDPattern = regexp.MustCompile(`^[a-z]{3}_[0-9A-Za-z]+$`)

// APIError represents an HTTP error response from the API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Body)
}

type APIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type SessionResponse struct {
	InstanceURL string `json:"instance_url"`
	JWT         string `json:"jwt"`
	CacheCold   bool   `json:"cache_cold"`
}

type CreateProjectResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Texlive string `json:"distribution_version"`
}

type SyncResult struct {
	Missing []string `json:"missing"`
}

type BuildDoneEvent struct {
	Status  string `json:"status"`
	PdfURL  string `json:"pdfUrl,omitempty"`
	Message string `json:"message,omitempty"`
	BuildID string `json:"build_id,omitempty"`
}

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type TokenResponse struct {
	JWT       string `json:"jwt"`
	ExpiresAt string `json:"expires_at"`
}

type TokenErrorResponse struct {
	Error string `json:"error"`
}

func readErrorBody(resp *http.Response) string {
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	s := strings.TrimSpace(string(text))
	var errResp struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(text, &errResp) == nil && errResp.Error != "" {
		return errResp.Error
	}
	return s
}

func NewAPIClient(baseURL, apiKey string) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func NewUnauthenticatedAPIClient(baseURL string) *APIClient {
	return NewAPIClient(baseURL, "")
}

func (c *APIClient) SetHTTPClient(hc *http.Client) {
	c.httpClient = hc
}

func (c *APIClient) CreateProject(name, distVersion, projectKey string) (CreateProjectResponse, error) {
	payload := map[string]string{
		"name":                 name,
		"distribution_version": distVersion,
	}
	if projectKey != "" {
		payload["project_key"] = projectKey
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CreateProjectResponse{}, err
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/projects", bytes.NewReader(body))
	if err != nil {
		return CreateProjectResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CreateProjectResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return CreateProjectResponse{}, fmt.Errorf("create project failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	var result CreateProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CreateProjectResponse{}, err
	}
	return result, nil
}

func (c *APIClient) GetSession(projectID, distributionVersion string) (SessionResponse, error) {
	u := fmt.Sprintf("%s/api/projects/%s/session", c.baseURL, projectID)

	body, err := json.Marshal(map[string]string{
		"distribution_version": distributionVersion,
	})
	if err != nil {
		return SessionResponse{}, err
	}

	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return SessionResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SessionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SessionResponse{}, fmt.Errorf("get session failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	var result SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SessionResponse{}, err
	}
	return result, nil
}

func (c *APIClient) RequestDeviceCode() (DeviceCodeResponse, error) {
	req, err := http.NewRequest("POST", c.baseURL+"/auth/device-code", nil)
	if err != nil {
		return DeviceCodeResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DeviceCodeResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceCodeResponse{}, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return DeviceCodeResponse{}, err
	}
	return result, nil
}

func (c *APIClient) PollToken(deviceCode string) (TokenResponse, error) {
	body, err := json.Marshal(map[string]string{"device_code": deviceCode})
	if err != nil {
		return TokenResponse{}, err
	}

	req, err := http.NewRequest("POST", c.baseURL+"/auth/token", bytes.NewReader(body))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result TokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return TokenResponse{}, err
		}
		return result, nil
	case http.StatusPreconditionRequired: // 428 — authorization pending
		return TokenResponse{}, ErrAuthorizationPending
	case http.StatusGone: // 410 — expired
		return TokenResponse{}, ErrDeviceCodeExpired
	default:
		return TokenResponse{}, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}
}

func (c *APIClient) RefreshToken(jwt string) (TokenResponse, error) {
	req, err := http.NewRequest("POST", c.baseURL+"/auth/refresh", nil)
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TokenResponse{}, err
	}
	return result, nil
}

type WhoamiResponse struct {
	UserID     string  `json:"user_id"`
	Email      string  `json:"email,omitempty"`
	AuthMethod string  `json:"auth_method"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
}

func (c *APIClient) Whoami() (WhoamiResponse, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/auth/whoami", nil)
	if err != nil {
		return WhoamiResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return WhoamiResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return WhoamiResponse{}, &APIError{StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	var result WhoamiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return WhoamiResponse{}, err
	}
	return result, nil
}

var (
	ErrAuthorizationPending = fmt.Errorf("authorization pending")
	ErrDeviceCodeExpired    = fmt.Errorf("device code expired")
	ErrTokenConflict        = fmt.Errorf("token name already exists")
	ErrTokenNotFound        = fmt.Errorf("token not found")
)

// APITokenResponse is the response from creating an API token.
type APITokenResponse struct {
	Token     string  `json:"token"`
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Prefix    string  `json:"prefix"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// APITokenListItem represents a token in a list response.
type APITokenListItem struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func (c *APIClient) CreateAPIToken(name string, expiresIn *int64) (APITokenResponse, error) {
	payload := map[string]any{"name": name}
	if expiresIn != nil {
		payload["expires_in"] = *expiresIn
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return APITokenResponse{}, err
	}

	req, err := http.NewRequest("POST", c.baseURL+"/auth/tokens", bytes.NewReader(body))
	if err != nil {
		return APITokenResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return APITokenResponse{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		var result APITokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return APITokenResponse{}, err
		}
		return result, nil
	case http.StatusConflict:
		return APITokenResponse{}, ErrTokenConflict
	default:
		return APITokenResponse{}, &APIError{StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}
}

func (c *APIClient) ListAPITokens() ([]APITokenListItem, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/auth/tokens", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	var result []APITokenListItem
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *APIClient) DeleteAPIToken(tokenID string) error {
	if !validIDPattern.MatchString(tokenID) {
		return fmt.Errorf("invalid token ID format")
	}
	req, err := http.NewRequest("DELETE", c.baseURL+"/auth/tokens/"+tokenID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrTokenNotFound
	default:
		return &APIError{StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}
}

type InstanceClient struct {
	baseURL    string
	jwt        string
	httpClient *http.Client
}

func NewInstanceClient(instanceURL, jwt string) *InstanceClient {
	return &InstanceClient{
		baseURL:    strings.TrimRight(instanceURL, "/"),
		jwt:        jwt,
		httpClient: &http.Client{},
	}
}

func (c *InstanceClient) SetHTTPClient(hc *http.Client) {
	c.httpClient = hc
}

func (c *InstanceClient) Sync(projectID string, files []FileEntry) (SyncResult, error) {
	body := struct {
		Files []FileEntry `json:"files"`
	}{Files: files}

	data, err := json.Marshal(body)
	if err != nil {
		return SyncResult{}, err
	}

	u := fmt.Sprintf("%s/projects/%s/sync", c.baseURL, projectID)

	req, err := http.NewRequest("POST", u, bytes.NewReader(data))
	if err != nil {
		return SyncResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SyncResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SyncResult{}, fmt.Errorf("sync failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	var result SyncResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

func (c *InstanceClient) Upload(projectID, projectDir string, filePaths []string, onProgress func(sent, total int64)) error {
	if len(filePaths) == 0 {
		return nil
	}

	tarData, err := createTar(projectDir, filePaths)
	if err != nil {
		return err
	}

	u := fmt.Sprintf("%s/projects/%s/upload", c.baseURL, projectID)

	var body io.Reader = bytes.NewReader(tarData)
	totalSize := int64(len(tarData))

	if onProgress != nil {
		body = &progressReader{
			reader: bytes.NewReader(tarData),
			total:  totalSize,
			onUpdate: func(fraction float64) {
				onProgress(int64(fraction*float64(totalSize)), totalSize)
			},
		}
	}

	req, err := http.NewRequest("POST", u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = totalSize

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}
	return nil
}

func (c *InstanceClient) UploadRaw(projectID string, tarData []byte) error {
	u := fmt.Sprintf("%s/projects/%s/upload", c.baseURL, projectID)

	req, err := http.NewRequest("POST", u, bytes.NewReader(tarData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}
	return nil
}

func (c *InstanceClient) BuildWithArgs(projectID, main, directory, distVersion, compiler string, args []string, buildOptions map[string]string, onLog func(string)) (BuildDoneEvent, error) {
	payload := map[string]any{
		"main":                 main,
		"distribution_version": distVersion,
	}
	if directory != "" {
		payload["directory"] = directory
	}
	if compiler != "" {
		payload["compiler"] = compiler
	}
	if len(args) > 0 {
		payload["args"] = args
	}
	if len(buildOptions) > 0 {
		payload["build_options"] = buildOptions
	}
	body, _ := json.Marshal(payload)

	u := fmt.Sprintf("%s/projects/%s/build", c.baseURL, projectID)

	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return BuildDoneEvent{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BuildDoneEvent{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return BuildDoneEvent{}, fmt.Errorf("build request failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	return ParseSSEStream(resp.Body, onLog)
}

func (c *InstanceClient) Build(projectID, main, directory, distVersion, compiler string, buildOptions map[string]string, onLog func(string)) (BuildDoneEvent, error) {
	payload := map[string]any{
		"main":                 main,
		"distribution_version": distVersion,
	}
	if directory != "" {
		payload["directory"] = directory
	}
	if compiler != "" {
		payload["compiler"] = compiler
	}
	if len(buildOptions) > 0 {
		payload["build_options"] = buildOptions
	}
	body, _ := json.Marshal(payload)

	u := fmt.Sprintf("%s/projects/%s/build", c.baseURL, projectID)

	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return BuildDoneEvent{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BuildDoneEvent{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return BuildDoneEvent{}, fmt.Errorf("build request failed (%d): %s", resp.StatusCode, readErrorBody(resp))
	}

	return ParseSSEStream(resp.Body, onLog)
}

func (c *InstanceClient) DownloadPDF(projectID, buildID, outputPath string) error {
	if !validIDPattern.MatchString(buildID) {
		return fmt.Errorf("invalid build ID format")
	}
	u := fmt.Sprintf("%s/projects/%s/builds/%s/output", c.baseURL, projectID, buildID)

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PDF download failed (%d)", resp.StatusCode)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(outputPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(outputPath)
		return closeErr
	}
	return nil
}

func ParseSSEStream(reader io.Reader, onLog func(string)) (BuildDoneEvent, error) {
	result := BuildDoneEvent{
		Status:  "error",
		Message: "Stream ended unexpectedly",
	}

	scanner := bufio.NewScanner(reader)
	var eventType, eventData string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if eventType != "" {
				switch eventType {
				case "log":
					if onLog != nil {
						onLog(extractSSEMessage(eventData))
					}
				case "queued":
					if onLog != nil {
						onLog(extractSSEMessage(eventData))
					}
				case "done":
					if err := json.Unmarshal([]byte(eventData), &result); err != nil {
						result = BuildDoneEvent{Status: "error", Message: eventData}
					}
				}
				eventType = ""
				eventData = ""
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = line[7:]
		} else if strings.HasPrefix(line, "data: ") {
			eventData = line[6:]
		}
	}

	if eventType == "done" {
		if err := json.Unmarshal([]byte(eventData), &result); err != nil {
			result = BuildDoneEvent{Status: "error", Message: eventData}
		}
	}

	return result, scanner.Err()
}

func extractSSEMessage(data string) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return data
}

func createTar(dir string, filePaths []string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, fp := range filePaths {
		data, err := os.ReadFile(filepath.Join(dir, fp))
		if err != nil {
			return nil, err
		}

		hdr := &tar.Header{
			Name: fp,
			Mode: 0644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
