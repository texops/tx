package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	Op         string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	if e.Op == "" {
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Body)
	}
	if e.Body == "" {
		return fmt.Sprintf("%s failed (%d)", e.Op, e.StatusCode)
	}
	return fmt.Sprintf("%s failed (%d): %s", e.Op, e.StatusCode, e.Body)
}

type APIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type SessionResponse struct {
	InstanceURL string `json:"instance_url"`
	JWT         string `json:"jwt"`
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
	Reason  string `json:"reason,omitempty"`
}

// Diagnostic is one LaTeX error or warning reported for a build.
type Diagnostic struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Context  string `json:"context,omitempty"`
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

func newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", currentUserAgent())
	return req, nil
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

func (c *APIClient) CreateProject(ctx context.Context, name, distVersion, projectKey string) (CreateProjectResponse, error) {
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

	req, err := newRequest(ctx, "POST", c.baseURL+"/api/projects", bytes.NewReader(body))
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
		return CreateProjectResponse{}, &APIError{Op: "create project", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	var result CreateProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CreateProjectResponse{}, err
	}
	return result, nil
}

func (c *APIClient) GetSession(ctx context.Context, projectID, distributionVersion string) (SessionResponse, error) {
	u := fmt.Sprintf("%s/api/projects/%s/session", c.baseURL, projectID)

	body, err := json.Marshal(map[string]string{
		"distribution_version": distributionVersion,
	})
	if err != nil {
		return SessionResponse{}, err
	}

	req, err := newRequest(ctx, "POST", u, bytes.NewReader(body))
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
		return SessionResponse{}, &APIError{Op: "get session", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	var result SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SessionResponse{}, err
	}
	return result, nil
}

func (c *APIClient) RequestDeviceCode() (DeviceCodeResponse, error) {
	req, err := newRequest(context.Background(), "POST", c.baseURL+"/auth/device-code", nil)
	if err != nil {
		return DeviceCodeResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DeviceCodeResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceCodeResponse{}, &APIError{Op: "device code request", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
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

	req, err := newRequest(context.Background(), "POST", c.baseURL+"/auth/token", bytes.NewReader(body))
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
		return TokenResponse{}, &APIError{Op: "token request", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}
}

func (c *APIClient) RefreshToken(jwt string) (TokenResponse, error) {
	req, err := newRequest(context.Background(), "POST", c.baseURL+"/auth/refresh", nil)
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
		return TokenResponse{}, &APIError{Op: "refresh", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
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
	req, err := newRequest(context.Background(), "GET", c.baseURL+"/auth/whoami", nil)
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

	req, err := newRequest(context.Background(), "POST", c.baseURL+"/auth/tokens", bytes.NewReader(body))
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
	req, err := newRequest(context.Background(), "GET", c.baseURL+"/auth/tokens", nil)
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
	req, err := newRequest(context.Background(), "DELETE", c.baseURL+"/auth/tokens/"+tokenID, nil)
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

// ErrStartUnsupported is returned by Start when the server has no start endpoint.
var ErrStartUnsupported = errors.New("server does not support sandbox start")

// Start brings the project's sandbox up, calling onLog with each progress message.
func (c *InstanceClient) Start(ctx context.Context, projectID string, onLog func(string)) error {
	u := fmt.Sprintf("%s/projects/%s/start", c.baseURL, projectID)
	req, err := newRequest(ctx, "POST", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrStartUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Op: "sandbox start", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	done, err := ParseSSEStream(resp.Body, onLog)
	if err != nil {
		return err
	}
	if done.Status != "success" {
		return errors.New(done.Message)
	}
	return nil
}

func (c *InstanceClient) Sync(ctx context.Context, projectID string, files []FileEntry) (SyncResult, error) {
	body := struct {
		Files []FileEntry `json:"files"`
	}{Files: files}

	data, err := json.Marshal(body)
	if err != nil {
		return SyncResult{}, err
	}

	u := fmt.Sprintf("%s/projects/%s/sync", c.baseURL, projectID)

	req, err := newRequest(ctx, "POST", u, bytes.NewReader(data))
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
		return SyncResult{}, &APIError{Op: "sync", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	var result SyncResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SyncResult{}, err
	}
	return result, nil
}

const maxUploadChunkBytes = 32 << 20

var uploadChunkBytes int64 = maxUploadChunkBytes

type uploadChunk struct {
	paths   []string
	tarSize int64
}

// Upload sends filePaths as a sequence of tar archives, each holding at most
// uploadChunkBytes of file data (a larger file is sent alone).
func (c *InstanceClient) Upload(ctx context.Context, projectID, projectDir string, filePaths []string, onProgress func(sent, total int64)) error {
	if len(filePaths) == 0 {
		return nil
	}

	chunks, total, err := planUploadChunks(projectDir, filePaths, uploadChunkBytes)
	if err != nil {
		return err
	}

	var sent int64
	for _, chunk := range chunks {
		tarData, err := createTar(ctx, projectDir, chunk.paths)
		if err != nil {
			return err
		}

		var onSent func(int64)
		if onProgress != nil {
			base := sent
			onSent = func(n int64) {
				onProgress(min(base+n, total), total)
			}
		}
		if err := c.uploadTar(ctx, projectID, tarData, onSent); err != nil {
			return err
		}
		sent += int64(len(tarData))
	}

	if onProgress != nil {
		onProgress(total, total)
	}
	return nil
}

func planUploadChunks(dir string, filePaths []string, limit int64) ([]uploadChunk, int64, error) {
	var chunks []uploadChunk
	var cur uploadChunk
	var curData, total int64

	flush := func() {
		cur.tarSize += 2 * tarBlockSize
		total += cur.tarSize
		chunks = append(chunks, cur)
		cur, curData = uploadChunk{}, 0
	}

	for _, fp := range filePaths {
		info, err := os.Stat(filepath.Join(dir, fp))
		if err != nil {
			return nil, 0, err
		}
		size := info.Size()

		if len(cur.paths) > 0 && curData+size > limit {
			flush()
		}

		hdrSize, err := tarHeaderSize(tarFileHeader(fp, size))
		if err != nil {
			return nil, 0, err
		}
		cur.paths = append(cur.paths, fp)
		cur.tarSize += hdrSize + (size+tarBlockSize-1)/tarBlockSize*tarBlockSize
		curData += size
	}
	flush()

	return chunks, total, nil
}

func (c *InstanceClient) UploadRaw(ctx context.Context, projectID string, tarData []byte) error {
	u := fmt.Sprintf("%s/projects/%s/upload", c.baseURL, projectID)

	req, err := newRequest(ctx, "POST", u, bytes.NewReader(tarData))
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
		return &APIError{Op: "upload", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}
	return nil
}

func (c *InstanceClient) BuildWithArgs(ctx context.Context, projectID, main, directory, distVersion, compiler string, args []string, buildOptions map[string]string, onLog func(string)) (BuildDoneEvent, error) {
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

	req, err := newRequest(ctx, "POST", u, bytes.NewReader(body))
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
		return BuildDoneEvent{}, &APIError{Op: "build request", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	return ParseSSEStream(resp.Body, onLog)
}

func (c *InstanceClient) Build(ctx context.Context, projectID, main, directory, distVersion, compiler string, buildOptions map[string]string, onLog func(string)) (BuildDoneEvent, error) {
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

	req, err := newRequest(ctx, "POST", u, bytes.NewReader(body))
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
		return BuildDoneEvent{}, &APIError{Op: "build request", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}

	return ParseSSEStream(resp.Body, onLog)
}

func (c *InstanceClient) DownloadPDF(ctx context.Context, projectID, buildID, outputPath string) error {
	if !validIDPattern.MatchString(buildID) {
		return fmt.Errorf("invalid build ID format")
	}
	u := fmt.Sprintf("%s/projects/%s/builds/%s/output", c.baseURL, projectID, buildID)

	req, err := newRequest(ctx, "GET", u, nil)
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
		return &APIError{Op: "PDF download", StatusCode: resp.StatusCode}
	}

	return writeFilePreserveInode(resp.Body, outputPath)
}

func (c *InstanceClient) uploadTar(ctx context.Context, projectID string, tarData []byte, onSent func(int64)) error {
	u := fmt.Sprintf("%s/projects/%s/upload", c.baseURL, projectID)

	var body io.Reader = bytes.NewReader(tarData)
	size := int64(len(tarData))

	if onSent != nil {
		body = &progressReader{
			reader: bytes.NewReader(tarData),
			total:  size,
			onUpdate: func(fraction float64) {
				onSent(int64(fraction * float64(size)))
			},
		}
	}

	req, err := newRequest(ctx, "POST", u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = size

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &APIError{Op: "upload", StatusCode: resp.StatusCode, Body: readErrorBody(resp)}
	}
	return nil
}

func writeFilePreserveInode(r io.Reader, outputPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".tx-download-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	tmpRead, err := os.Open(tmpPath)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		_ = tmpRead.Close()
		return err
	}

	_, copyErr = io.Copy(out, tmpRead)
	_ = tmpRead.Close()
	closeErr = out.Close()

	if copyErr != nil {
		return copyErr
	}
	return closeErr
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

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			eventData = after
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

func createTar(ctx context.Context, dir string, filePaths []string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, fp := range filePaths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		data, err := os.ReadFile(filepath.Join(dir, fp))
		if err != nil {
			return nil, err
		}

		if err := tw.WriteHeader(tarFileHeader(fp, int64(len(data)))); err != nil {
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

const tarBlockSize = 512

func tarFileHeader(name string, size int64) *tar.Header {
	return &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: size,
	}
}

type countingWriter struct {
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func tarHeaderSize(hdr *tar.Header) (int64, error) {
	var cw countingWriter
	if err := tar.NewWriter(&cw).WriteHeader(hdr); err != nil {
		return 0, err
	}
	return cw.n, nil
}
