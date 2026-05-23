package s3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

var _ runstate.BlobAdmin = (*Store)(nil)

const (
	serviceName = "s3"
	algorithm   = "AWS4-HMAC-SHA256"
)

var (
	bucketPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{1,61}[A-Za-z0-9]$`)
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9._=/!-]*$`)
	blobIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Client          *http.Client
}

type Store struct {
	endpoint        *url.URL
	bucket          string
	region          string
	prefix          string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	client          *http.Client
}

func NewStore(config Config) (*Store, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if !bucketPattern.MatchString(config.Bucket) {
		return nil, fmt.Errorf("s3 blob: invalid bucket %q", config.Bucket)
	}
	if config.Region == "" {
		return nil, fmt.Errorf("s3 blob: region is required")
	}
	if config.AccessKeyID == "" {
		return nil, fmt.Errorf("s3 blob: access key id is required")
	}
	if config.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3 blob: secret access key is required")
	}
	prefix, err := cleanPrefix(config.Prefix)
	if err != nil {
		return nil, err
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &Store{
		endpoint:        endpoint,
		bucket:          config.Bucket,
		region:          config.Region,
		prefix:          prefix,
		accessKeyID:     config.AccessKeyID,
		secretAccessKey: config.SecretAccessKey,
		sessionToken:    config.SessionToken,
		client:          client,
	}, nil
}

func (s *Store) Put(ctx context.Context, data []byte) (runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return runstate.BlobRef{}, err
	}
	ref := runstate.NewBlobRef("", data)
	ref.ID = ref.Sha256
	requestURL := s.objectURL(ref.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL.String(), bytes.NewReader(data))
	if err != nil {
		return runstate.BlobRef{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := s.sign(req, ref.Sha256, time.Now().UTC()); err != nil {
		return runstate.BlobRef{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return runstate.BlobRef{}, fmt.Errorf("s3 blob: put %s: %w", ref.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return runstate.BlobRef{}, responseError("put", ref.ID, resp)
	}
	return ref, nil
}

func (s *Store) Get(ctx context.Context, ref runstate.BlobRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !blobIDPattern.MatchString(ref.ID) {
		return nil, fmt.Errorf("s3 blob: invalid blob id %q", ref.ID)
	}
	requestURL := s.objectURL(ref.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	payloadHash := sha256Hex(nil)
	if err := s.sign(req, payloadHash, time.Now().UTC()); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 blob: get %s: %w", ref.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, runstate.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("get", ref.ID, resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 blob: read %s: %w", ref.ID, err)
	}
	got := sha256Hex(data)
	if ref.Sha256 != "" && got != ref.Sha256 {
		return nil, fmt.Errorf("s3 blob: checksum mismatch for %s", ref.ID)
	}
	if ref.Size > 0 && int64(len(data)) != ref.Size {
		return nil, fmt.Errorf("s3 blob: size mismatch for %s", ref.ID)
	}
	return data, nil
}

func (s *Store) List(ctx context.Context) ([]runstate.BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requestURL := s.listURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	payloadHash := sha256Hex(nil)
	if err := s.sign(req, payloadHash, time.Now().UTC()); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 blob: list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("list", "", resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("s3 blob: read list response: %w", err)
	}
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("s3 blob: decode list response: %w", err)
	}
	out := make([]runstate.BlobRef, 0, len(result.Contents))
	for _, item := range result.Contents {
		id, ok := s.blobIDFromObjectKey(item.Key)
		if !ok {
			continue
		}
		out = append(out, runstate.BlobRef{ID: id, Size: item.Size, Sha256: id})
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, ref runstate.BlobRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !blobIDPattern.MatchString(ref.ID) {
		return fmt.Errorf("s3 blob: invalid blob id %q", ref.ID)
	}
	requestURL := s.objectURL(ref.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, requestURL.String(), nil)
	if err != nil {
		return err
	}
	payloadHash := sha256Hex(nil)
	if err := s.sign(req, payloadHash, time.Now().UTC()); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 blob: delete %s: %w", ref.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return responseError("delete", ref.ID, resp)
	}
	return nil
}

func (s *Store) objectURL(id string) url.URL {
	objectKey := id + ".blob"
	if s.prefix != "" {
		objectKey = path.Join(s.prefix, objectKey)
	}
	u := *s.endpoint
	u.Path = path.Join(u.Path, s.bucket, objectKey)
	return u
}

func (s *Store) listURL() url.URL {
	u := *s.endpoint
	u.Path = path.Join(u.Path, s.bucket)
	query := url.Values{}
	query.Set("list-type", "2")
	if s.prefix != "" {
		query.Set("prefix", s.prefix+"/")
	}
	u.RawQuery = query.Encode()
	return u
}

func (s *Store) blobIDFromObjectKey(key string) (string, bool) {
	if s.prefix != "" {
		prefix := s.prefix + "/"
		if !strings.HasPrefix(key, prefix) {
			return "", false
		}
		key = strings.TrimPrefix(key, prefix)
	}
	if !strings.HasSuffix(key, ".blob") {
		return "", false
	}
	id := strings.TrimSuffix(key, ".blob")
	if !blobIDPattern.MatchString(id) {
		return "", false
	}
	return id, true
}

type listBucketResult struct {
	Contents []listObject `xml:"Contents"`
}

type listObject struct {
	Key  string `xml:"Key"`
	Size int64  `xml:"Size"`
}

func (s *Store) sign(req *http.Request, payloadHash string, now time.Time) error {
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)
	if s.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.sessionToken)
	}
	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	credentialScope := date + "/" + s.region + "/" + serviceName + "/aws4_request"
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	requestHash := sha256Hex([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{algorithm, amzDate, credentialScope, requestHash}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(s.secretAccessKey, date, s.region), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s", algorithm, s.accessKeyID, credentialScope, signedHeaders, signature))
	return nil
}

func canonicalHeaders(req *http.Request) (string, string) {
	headers := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("X-Amz-Security-Token") != "" {
		headers = append(headers, "x-amz-security-token")
	}
	var builder strings.Builder
	for _, header := range headers {
		value := req.URL.Host
		if header != "host" {
			value = req.Header.Get(header)
		}
		builder.WriteString(header)
		builder.WriteByte(':')
		builder.WriteString(strings.Join(strings.Fields(value), " "))
		builder.WriteByte('\n')
	}
	return builder.String(), strings.Join(headers, ";")
}

func canonicalURI(u *url.URL) string {
	uri := u.EscapedPath()
	if uri == "" {
		return "/"
	}
	return uri
}

func signingKey(secret, date, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte(serviceName))
	return hmacSHA256(serviceKey, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func responseError(operation, id string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	body := strings.TrimSpace(string(data))
	if body == "" {
		return fmt.Errorf("s3 blob: %s %s failed with status %s", operation, id, resp.Status)
	}
	return fmt.Errorf("s3 blob: %s %s failed with status %s: %s", operation, id, resp.Status, body)
}

func parseEndpoint(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("s3 blob: endpoint is required")
	}
	endpoint, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("s3 blob: parse endpoint: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("s3 blob: endpoint must use http or https")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("s3 blob: endpoint host is required")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("s3 blob: endpoint must not include query or fragment")
	}
	return endpoint, nil
}

func cleanPrefix(prefix string) (string, error) {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	if !prefixPattern.MatchString(prefix) {
		return "", fmt.Errorf("s3 blob: invalid prefix %q", prefix)
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("s3 blob: invalid prefix %q", prefix)
		}
	}
	return prefix, nil
}
