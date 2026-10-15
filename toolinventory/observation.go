package toolinventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
)

const (
	versionHTTPLimit = 256 * 1024
	versionFreshTTL  = 6 * time.Hour
	versionRetryTTL  = time.Minute
)

type VersionSource string

const (
	VersionSourceUnsupported VersionSource = "unsupported_source"
	VersionSourceNPMRegistry VersionSource = "npm_registry"
	VersionSourceGrokCheck   VersionSource = "grok_update_check"
)

type VersionState string

const (
	VersionStateUnsupported VersionState = "unsupported_source"
	VersionStateFailed      VersionState = "failed"
	VersionStateCurrent     VersionState = "current"
	VersionStateUpdate      VersionState = "update_available"
	VersionStateNewer       VersionState = "installed_newer"
)

type VersionObservation struct {
	AgentID            string         `json:"agentId"`
	State              VersionState   `json:"state"`
	CheckedAt          time.Time      `json:"checkedAt"`
	FreshUntil         time.Time      `json:"freshUntil"`
	Source             VersionSource  `json:"source"`
	Channel            string         `json:"channel,omitempty"`
	InstallFingerprint string         `json:"installFingerprint"`
	InstalledVersion   string         `json:"installedVersion,omitempty"`
	TargetVersion      string         `json:"targetVersion,omitempty"`
	ErrorCode          string         `json:"errorCode,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	UpdateRecipe       *CommandRecipe `json:"updateRecipe,omitempty"`
}

type CheckTarget struct {
	Product
	ArtifactPath       string
	Origin             InstallOrigin
	InstalledVersion   string
	Scope              string
	InstallFingerprint string
}

type GrokUpdateCheck struct {
	CurrentVersion string
	LatestVersion  string
	Update         bool
	Installer      string
	Channel        string
	AutoUpdate     bool
}

type VersionDependencies struct {
	Now       func() time.Time
	NPMLatest func(context.Context, string) (string, error)
	GrokCheck func(context.Context, string) (GrokUpdateCheck, error)
}

type VersionService struct {
	mu         sync.Mutex
	entries    map[string]*versionEntry
	latestKeys map[string]string
	tokens     chan struct{}
	deps       VersionDependencies
}

type versionEntry struct {
	ready         chan struct{}
	observation   VersionObservation
	retryNotAfter time.Time
}

func NewVersionService(deps VersionDependencies) *VersionService {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NPMLatest == nil {
		client := &http.Client{Timeout: 10 * time.Second}
		deps.NPMLatest = func(ctx context.Context, pkg string) (string, error) {
			return npmLatest(ctx, client, pkg)
		}
	}
	if deps.GrokCheck == nil {
		deps.GrokCheck = runGrokUpdateCheck
	}
	return &VersionService{
		entries:    make(map[string]*versionEntry),
		latestKeys: make(map[string]string),
		tokens:     make(chan struct{}, 2),
		deps:       deps,
	}
}

func (s *VersionService) Snapshot(agentID string) (VersionObservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[observationKeyByAgent(agentID)]
	if entry == nil {
		return VersionObservation{}, false
	}
	select {
	case <-entry.ready:
		return entry.observation, true
	default:
		return VersionObservation{}, false
	}
}

func (s *VersionService) Check(ctx context.Context, target CheckTarget, force bool) VersionObservation {
	source := versionSource(target)
	key := versionKey(target, source)
	now := s.deps.Now()
	s.mu.Lock()
	entry := s.entries[key]
	if entry != nil {
		select {
		case <-entry.ready:
			if !force && s.canReuse(entry, now) {
				s.mu.Unlock()
				return entry.observation
			}
		default:
			ready := entry.ready
			s.mu.Unlock()
			select {
			case <-ready:
				s.mu.Lock()
				if current := s.entries[key]; current != nil {
					observation := current.observation
					s.mu.Unlock()
					return observation
				}
				s.mu.Unlock()
			case <-ctx.Done():
				return failedObservation(target, source, "context_canceled", "version check was canceled")
			}
			s.mu.Lock()
		}
	}
	entry = &versionEntry{ready: make(chan struct{})}
	s.entries[key] = entry
	s.latestKeys[target.ID] = key
	s.mu.Unlock()

	observation := s.perform(ctx, target, source)
	now = s.deps.Now()
	observation.CheckedAt = now
	if observation.State != VersionStateFailed {
		observation.FreshUntil = now.Add(versionFreshTTL)
	}
	entry.observation = observation
	entry.retryNotAfter = now.Add(versionRetryTTL)
	close(entry.ready)
	s.mu.Lock()
	if s.entries[key] == entry && s.latestKeys[target.ID] == key {
		s.setAgentSnapshot(target.ID, observation)
	}
	s.mu.Unlock()
	return observation
}

func (s *VersionService) perform(ctx context.Context, target CheckTarget, source VersionSource) VersionObservation {
	if source == VersionSourceUnsupported {
		observation := unsupportedObservation(target, source)
		observation.CheckedAt = s.deps.Now()
		return observation
	}
	select {
	case s.tokens <- struct{}{}:
		defer func() { <-s.tokens }()
	case <-ctx.Done():
		return failedObservation(target, source, "context_canceled", "version check was canceled")
	}
	switch source {
	case VersionSourceNPMRegistry:
		latest, err := s.deps.NPMLatest(ctx, target.NPMPackage)
		if err != nil {
			return failedObservation(target, source, npmErrorCode(err), npmErrorReason(err))
		}
		return compareForUpdate(target, source, "latest", latest)
	case VersionSourceGrokCheck:
		result, err := s.deps.GrokCheck(ctx, target.ArtifactPath)
		if err != nil {
			return failedObservation(target, source, grokErrorCode(err), grokErrorReason(err))
		}
		return compareGrok(target, source, result)
	default:
		return unsupportedObservation(target, source)
	}
}

func (s *VersionService) canReuse(entry *versionEntry, now time.Time) bool {
	if !entry.observation.FreshUntil.IsZero() && now.Before(entry.observation.FreshUntil) {
		return true
	}
	return entry.observation.State == VersionStateFailed && now.Before(entry.retryNotAfter)
}

func (s *VersionService) setAgentSnapshot(agentID string, observation VersionObservation) {
	observation.AgentID = agentID
	s.entries[observationKeyByAgent(agentID)] = &versionEntry{
		ready:         make(chan struct{}),
		observation:   observation,
		retryNotAfter: s.deps.Now().Add(versionRetryTTL),
	}
	close(s.entries[observationKeyByAgent(agentID)].ready)
}

func versionSource(target CheckTarget) VersionSource {
	if target.Product.ID == "grokbuild" && target.ArtifactPath != "" {
		return VersionSourceGrokCheck
	}
	if target.Origin.Method == InstallMethodNPM && target.NPMPackage != "" {
		return VersionSourceNPMRegistry
	}
	return VersionSourceUnsupported
}

func compareForUpdate(target CheckTarget, source VersionSource, channel, latest string) VersionObservation {
	if _, err := semver.StrictNewVersion(strings.TrimPrefix(latest, "v")); err != nil {
		return failedObservation(target, source, "invalid_target_version", "release source returned an invalid semantic version")
	}
	comparison, err := CompareStrict(target.InstalledVersion, latest)
	if err != nil {
		return failedObservation(target, source, "invalid_installed_version", "installed package metadata is not a valid semantic version")
	}
	observation := VersionObservation{
		AgentID: target.ID, Source: source, Channel: channel,
		InstallFingerprint: target.InstallFingerprint, InstalledVersion: target.InstalledVersion,
		TargetVersion: latest,
	}
	switch {
	case comparison < 0:
		observation.State = VersionStateUpdate
		recipe, err := BuildNPMUpdateRecipe(target.Origin, latest, target.Scope)
		if err != nil {
			return failedObservation(target, source, "recipe_failed", "the verified target cannot be represented safely for this install scope")
		}
		observation.UpdateRecipe = &recipe
	case comparison == 0:
		observation.State = VersionStateCurrent
	default:
		observation.State = VersionStateNewer
	}
	return observation
}

func compareGrok(target CheckTarget, source VersionSource, result GrokUpdateCheck) VersionObservation {
	observation := VersionObservation{
		AgentID: target.ID, Source: source, Channel: result.Channel,
		InstallFingerprint: target.InstallFingerprint, InstalledVersion: result.CurrentVersion,
		TargetVersion: result.LatestVersion,
	}
	comparison, err := CompareStrict(result.CurrentVersion, result.LatestVersion)
	if err != nil {
		return failedObservation(target, source, "invalid_grok_version", "Grok check returned an invalid semantic version")
	}
	switch {
	case result.Update && comparison < 0:
		observation.State = VersionStateUpdate
		recipe, recipeErr := BuildGrokUpdateRecipe(target.ArtifactPath, result.LatestVersion)
		if recipeErr != nil {
			return failedObservation(target, source, "recipe_failed", "the verified target cannot be represented safely for this install scope")
		}
		observation.UpdateRecipe = &recipe
	case result.Update && comparison >= 0:
		return failedObservation(target, source, "target_not_newer", "Grok reports an update but its target is not newer than the installed version")
	case comparison < 0:
		return failedObservation(target, source, "grok_policy_conflict", "Grok reports no update although a newer target was returned")
	case comparison == 0:
		observation.State = VersionStateCurrent
	default:
		observation.State = VersionStateNewer
	}
	return observation
}

func unsupportedObservation(target CheckTarget, source VersionSource) VersionObservation {
	return VersionObservation{
		AgentID: target.ID, State: VersionStateUnsupported, Source: source,
		InstallFingerprint: target.InstallFingerprint, InstalledVersion: target.InstalledVersion,
		Reason: "this installation source has no verified latest-version provider",
	}
}

func failedObservation(target CheckTarget, source VersionSource, code, reason string) VersionObservation {
	return VersionObservation{
		AgentID: target.ID, State: VersionStateFailed, Source: source,
		InstallFingerprint: target.InstallFingerprint, InstalledVersion: target.InstalledVersion,
		ErrorCode: code, Reason: reason,
	}
}

func observationKeyByAgent(agentID string) string { return "agent:" + agentID }

func versionKey(target CheckTarget, source VersionSource) string {
	return strings.Join([]string{target.ID, string(source), target.InstallFingerprint}, "\x00")
}

func npmLatest(ctx context.Context, client *http.Client, pkg string) (string, error) {
	return npmLatestFromRegistry(ctx, client, "https://registry.npmjs.org", pkg)
}

func npmLatestFromRegistry(ctx context.Context, client *http.Client, baseURL, pkg string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+url.PathEscape(pkg)+"/latest", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", httpStatusError{status: response.StatusCode}
	}
	version, err := decodeNPMRegistryDocument(response.Body)
	if err != nil {
		return "", decodeError{err: err}
	}
	return version, nil
}

func decodeNPMRegistryDocument(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, versionHTTPLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > versionHTTPLimit {
		return "", errors.New("response exceeds size limit")
	}
	var root struct {
		Version string `json:"version"`
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return "", err
	}
	rawVersion, ok := object["version"]
	if !ok {
		return "", errors.New("missing version field")
	}
	if err := json.Unmarshal(rawVersion, &root.Version); err != nil {
		return "", err
	}
	if strings.TrimSpace(root.Version) == "" {
		return "", errors.New("missing non-empty version string")
	}
	return root.Version, nil
}

type httpStatusError struct{ status int }

func (e httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

type decodeError struct{ err error }

func (e decodeError) Error() string { return e.err.Error() }

func npmErrorCode(err error) string {
	var status httpStatusError
	var decode decodeError
	switch {
	case errors.As(err, &status):
		return "npm_http_status"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &decode):
		return "npm_invalid_response"
	default:
		return "network"
	}
}

func npmErrorReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "npm registry request timed out"
	}
	return "npm registry request failed"
}

func ArtifactFingerprint(productID, realPath, manifestPath string, info os.FileInfo, manifestInfo os.FileInfo) string {
	hash := sha256.New()
	for _, value := range []string{productID, realPath, manifestPath} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	if info != nil {
		_, _ = fmt.Fprintf(hash, "%d:%d\n", info.Size(), info.ModTime().UnixNano())
	}
	if manifestInfo != nil {
		_, _ = fmt.Fprintf(hash, "%d:%d\n", manifestInfo.Size(), manifestInfo.ModTime().UnixNano())
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
