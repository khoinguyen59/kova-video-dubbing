package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const voicePreviewText = "Xin chào, rất vui được gặp bạn, hẹn gặp lại."

// savedVoiceProfile is private desktop state. It never contains a worker
// token, the original source path, or a reference-audio path supplied by the
// UI. ReferenceFile is a filename relative to KOVA's private voice library.
type savedVoiceProfile struct {
	LibraryID       string `json:"library_id"`
	RemoteProfileID string `json:"remote_profile_id"`
	Name            string `json:"name"`
	Language        string `json:"language"`
	ReferenceFile   string `json:"reference_file,omitempty"`
	WorkerURL       string `json:"worker_url,omitempty"`
	CreatedAt       string `json:"created_at"`
}

type voiceProfileLibrary struct {
	Version  int                 `json:"version"`
	Profiles []savedVoiceProfile `json:"profiles"`
}

func (a *App) voiceLibraryDir() string {
	return filepath.Join(a.desktopProjectDataRoot(), "voice-library")
}

func (a *App) voiceLibraryManifestPath() string {
	return filepath.Join(a.voiceLibraryDir(), "profiles.json")
}

func (a *App) loadVoiceProfileLibrary() (voiceProfileLibrary, error) {
	path := a.voiceLibraryManifestPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return voiceProfileLibrary{Version: 1}, nil
	}
	if err != nil {
		return voiceProfileLibrary{}, fmt.Errorf("read KOVA voice library: %w", err)
	}
	var library voiceProfileLibrary
	if err := json.Unmarshal(data, &library); err != nil {
		return voiceProfileLibrary{}, fmt.Errorf("decode KOVA voice library: %w", err)
	}
	if library.Version == 0 {
		library.Version = 1
	}
	return library, nil
}

func (a *App) writeVoiceProfileLibrary(library voiceProfileLibrary) error {
	if err := os.MkdirAll(a.voiceLibraryDir(), 0700); err != nil {
		return fmt.Errorf("create KOVA voice library: %w", err)
	}
	library.Version = 1
	data, err := json.MarshalIndent(library, "", "  ")
	if err != nil {
		return fmt.Errorf("encode KOVA voice library: %w", err)
	}
	path := a.voiceLibraryManifestPath()
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write KOVA voice library: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace KOVA voice library: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("finalize KOVA voice library: %w", err)
	}
	return nil
}

func voiceReferenceFilename(libraryID, extension string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != ".wav" && extension != ".mp3" && extension != ".flac" {
		extension = ".wav"
	}
	safeID := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, libraryID)
	return safeID + extension
}

func copyVoiceReference(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	temporaryPath := destination + ".tmp"
	output, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temporaryPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporaryPath)
		return closeErr
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporaryPath)
		return err
	}
	return os.Rename(temporaryPath, destination)
}

// saveVoiceProfileBackup records a stable selection ID and a private copy of
// the consented sample. The worker token is deliberately absent: a fresh Colab
// URL/token is still required after a Colab session has ended.
func (a *App) saveVoiceProfileBackup(profile VoiceProfile, workerURL, sourceAudio string) (VoiceProfile, error) {
	library, err := a.loadVoiceProfileLibrary()
	if err != nil {
		return VoiceProfile{}, err
	}
	libraryID := strings.TrimSpace(profile.ID)
	if libraryID == "" {
		return VoiceProfile{}, errors.New("cannot save a voice profile without an id")
	}
	referenceFile := voiceReferenceFilename(libraryID, filepath.Ext(sourceAudio))
	if err := copyVoiceReference(sourceAudio, filepath.Join(a.voiceLibraryDir(), "references", referenceFile)); err != nil {
		return VoiceProfile{}, fmt.Errorf("save consented voice reference locally: %w", err)
	}
	record := savedVoiceProfile{
		LibraryID:       libraryID,
		RemoteProfileID: libraryID,
		Name:            strings.TrimSpace(profile.Name),
		Language:        firstNonEmpty(strings.TrimSpace(profile.Language), "vi"),
		ReferenceFile:   referenceFile,
		WorkerURL:       strings.TrimSpace(workerURL),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	replaced := false
	for index := range library.Profiles {
		if library.Profiles[index].LibraryID == libraryID {
			// Preserve a current restored remote ID if the user updates a backup.
			if library.Profiles[index].RemoteProfileID != "" {
				record.RemoteProfileID = library.Profiles[index].RemoteProfileID
			}
			library.Profiles[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		library.Profiles = append(library.Profiles, record)
	}
	if err := a.writeVoiceProfileLibrary(library); err != nil {
		return VoiceProfile{}, err
	}
	profile.Saved = true
	profile.BackupAvailable = true
	profile.WorkerURL = strings.TrimSpace(workerURL)
	return profile, nil
}

func (a *App) listRemoteVoiceProfiles(baseURL, token string) ([]VoiceProfile, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/v1/voices?status=ready", nil)
	if err != nil {
		return nil, err
	}
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("cannot load Voice Studio profiles: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Voice Studio returned %s", response.Status)
	}
	var profiles []VoiceProfile
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode Voice Studio profiles: %w", err)
	}
	return profiles, nil
}

// importRemoteVoiceBackup copies a reference from an authenticated Voice
// Studio worker into KOVA's private library. Older workers may not expose this
// endpoint yet; callers deliberately keep the profile metadata in that case.
func (a *App) importRemoteVoiceBackup(baseURL, token string, profile VoiceProfile) error {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/v1/profiles/"+url.PathEscape(profile.ID)+"/reference", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Voice Studio reference export returned %s", response.Status)
	}
	filename := "reference.wav"
	if _, params, parseErr := mime.ParseMediaType(response.Header.Get("Content-Disposition")); parseErr == nil && params["filename"] != "" {
		filename = params["filename"]
	}
	extension := filepath.Ext(filename)
	if extension != ".wav" && extension != ".mp3" && extension != ".flac" {
		extension = ".wav"
	}
	incomingDir := filepath.Join(a.voiceLibraryDir(), "incoming")
	if err := os.MkdirAll(incomingDir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(incomingDir, "profile-reference-*"+extension)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, 256*1024*1024+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || written > 256*1024*1024 {
		return errors.New("Voice Studio reference export is empty or too large")
	}
	_, err = a.saveVoiceProfileBackup(profile, baseURL, temporaryPath)
	return err
}

func (a *App) ListVoiceProfiles(request VoiceHealthRequest) ([]VoiceProfile, error) {
	library, err := a.loadVoiceProfileLibrary()
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSpace(request.BaseURL)
	var remote []VoiceProfile
	if baseURL != "" {
		baseURL, err = normalizeVoiceURL(baseURL)
		if err != nil {
			return nil, err
		}
		remote, err = a.listRemoteVoiceProfiles(baseURL, request.Token)
		if err != nil {
			return nil, err
		}
		// Try to archive a reference for profiles created by an older KOVA
		// version. If the Colab worker predates its authenticated export endpoint,
		// the following metadata-only import still preserves the profile list.
		backedUp := make(map[string]bool, len(library.Profiles))
		for _, record := range library.Profiles {
			backedUp[record.RemoteProfileID] = strings.TrimSpace(record.ReferenceFile) != ""
		}
		for _, profile := range remote {
			if strings.TrimSpace(profile.ID) == "" || backedUp[profile.ID] {
				continue
			}
			_ = a.importRemoteVoiceBackup(baseURL, request.Token, profile)
		}
		library, err = a.loadVoiceProfileLibrary()
		if err != nil {
			return nil, err
		}
		knownRemoteIDs := make(map[string]bool, len(library.Profiles))
		for _, record := range library.Profiles {
			knownRemoteIDs[record.RemoteProfileID] = true
		}
		changed := false
		for _, profile := range remote {
			if strings.TrimSpace(profile.ID) == "" || knownRemoteIDs[profile.ID] {
				continue
			}
			library.Profiles = append(library.Profiles, savedVoiceProfile{
				LibraryID:       profile.ID,
				RemoteProfileID: profile.ID,
				Name:            profile.Name,
				Language:        firstNonEmpty(strings.TrimSpace(profile.Language), "vi"),
				WorkerURL:       baseURL,
				CreatedAt:       time.Now().UTC().Format(time.RFC3339),
			})
			knownRemoteIDs[profile.ID] = true
			changed = true
		}
		if changed {
			if err := a.writeVoiceProfileLibrary(library); err != nil {
				return nil, err
			}
		}
	}

	remoteByID := make(map[string]VoiceProfile, len(remote))
	for _, profile := range remote {
		remoteByID[profile.ID] = profile
	}
	profiles := make([]VoiceProfile, 0, len(library.Profiles)+len(remote))
	seenRemote := make(map[string]bool, len(library.Profiles))
	for _, record := range library.Profiles {
		if strings.TrimSpace(record.LibraryID) == "" {
			continue
		}
		profile := VoiceProfile{ID: record.LibraryID, Name: record.Name, Language: record.Language, Status: "saved_local", Saved: true, BackupAvailable: record.ReferenceFile != "", WorkerURL: record.WorkerURL}
		if current, exists := remoteByID[record.RemoteProfileID]; exists {
			profile.Name = firstNonEmpty(strings.TrimSpace(current.Name), profile.Name)
			profile.Language = firstNonEmpty(strings.TrimSpace(current.Language), profile.Language)
			profile.Status = current.Status
			seenRemote[record.RemoteProfileID] = true
		}
		profiles = append(profiles, profile)
	}
	for _, profile := range remote {
		if seenRemote[profile.ID] {
			continue
		}
		profile.WorkerURL = baseURL
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

// ensureVoiceProfileOnWorker resolves the stable KOVA library selection to a
// profile currently available on this Colab worker. If Colab was reset, it
// reconstructs the remote profile from the consented local backup.
func (a *App) ensureVoiceProfileOnWorker(libraryID, workerURL, token string) (string, error) {
	libraryID = strings.TrimSpace(libraryID)
	library, err := a.loadVoiceProfileLibrary()
	if err != nil {
		return "", err
	}
	var record *savedVoiceProfile
	for index := range library.Profiles {
		if library.Profiles[index].LibraryID == libraryID {
			record = &library.Profiles[index]
			break
		}
	}
	// Remote-only profiles from an existing Voice Studio retain the historical
	// direct behavior. KOVA cannot restore one until its reference is imported.
	if record == nil {
		return libraryID, nil
	}
	remote, err := a.listRemoteVoiceProfiles(workerURL, token)
	if err != nil {
		return "", err
	}
	for _, profile := range remote {
		if profile.ID == record.RemoteProfileID {
			return profile.ID, nil
		}
	}
	if strings.TrimSpace(record.ReferenceFile) == "" {
		return "", errors.New("profile này chỉ có trên Colab cũ và chưa có audio sao lưu cục bộ; hãy tạo lại một lần với KOVA 1.0.2.6 để KOVA lưu bản khôi phục")
	}
	referencePath := filepath.Join(a.voiceLibraryDir(), "references", filepath.Base(record.ReferenceFile))
	if info, statErr := os.Stat(referencePath); statErr != nil || info.IsDir() || info.Size() == 0 {
		return "", errors.New("không tìm thấy audio sao lưu của profile; hãy chọn audio mẫu và tạo lại profile")
	}
	restored, err := a.CreateVoiceProfile(VoiceProfileCreateRequest{
		BaseURL:            workerURL,
		Token:              token,
		Name:               record.Name,
		ReferenceAudioPath: referencePath,
		Language:           record.Language,
		ConsentConfirmed:   true,
	})
	if err != nil {
		return "", fmt.Errorf("khôi phục profile clone trên Colab: %w", err)
	}
	updated, err := a.loadVoiceProfileLibrary()
	if err != nil {
		return "", err
	}
	for index := range updated.Profiles {
		if updated.Profiles[index].LibraryID == libraryID {
			updated.Profiles[index].RemoteProfileID = restored.ID
			updated.Profiles[index].WorkerURL = workerURL
			break
		}
	}
	// CreateVoiceProfile added a transient record for the newly assigned remote
	// id. Remove it so the user's original dropdown selection remains stable.
	filtered := updated.Profiles[:0]
	for _, candidate := range updated.Profiles {
		if candidate.LibraryID == restored.ID && candidate.LibraryID != libraryID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	updated.Profiles = filtered
	if err := a.writeVoiceProfileLibrary(updated); err != nil {
		return "", err
	}
	return restored.ID, nil
}

// PreviewVoiceProfile synthesizes a deliberately short fixed sentence. It
// lets the user hear an existing saved/clone profile before starting a long
// dubbing job. The audio is returned as an in-memory data URL, never written
// into a project or persisted with a worker token.
func (a *App) PreviewVoiceProfile(request VoicePreviewRequest) (VoicePreview, error) {
	workerURL, err := normalizeVoiceURL(request.BaseURL)
	if err != nil {
		return VoicePreview{}, err
	}
	if strings.TrimSpace(request.Token) == "" {
		return VoicePreview{}, errors.New("paste the current Voice Studio Colab token before previewing a cloned voice")
	}
	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		return VoicePreview{}, errors.New("select a voice profile to preview")
	}
	remoteID, err := a.ensureVoiceProfileOnWorker(profileID, workerURL, strings.TrimSpace(request.Token))
	if err != nil {
		return VoicePreview{}, err
	}
	body := &strings.Builder{}
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{
		"text": voicePreviewText, "profile_id": remoteID, "language": firstNonEmpty(strings.TrimSpace(request.Language), "vi"), "speed": "1.0", "num_step": "32", "output_format": "wav",
	} {
		if err := writer.WriteField(key, value); err != nil {
			return VoicePreview{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return VoicePreview{}, err
	}
	httpRequest, err := http.NewRequest(http.MethodPost, workerURL+"/generate", strings.NewReader(body.String()))
	if err != nil {
		return VoicePreview{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(request.Token))
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	client := a.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return VoicePreview{}, fmt.Errorf("generate voice preview: %w", err)
	}
	defer response.Body.Close()
	const maxPreviewBytes = 8 * 1024 * 1024
	audio, readErr := io.ReadAll(io.LimitReader(response.Body, maxPreviewBytes+1))
	if readErr != nil {
		return VoicePreview{}, fmt.Errorf("read voice preview: %w", readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return VoicePreview{}, fmt.Errorf("Voice Studio preview returned %s: %s", response.Status, strings.TrimSpace(string(audio)))
	}
	if len(audio) == 0 || len(audio) > maxPreviewBytes {
		return VoicePreview{}, errors.New("Voice Studio returned an empty or oversized preview audio")
	}
	return VoicePreview{ProfileID: profileID, DataURL: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audio)}, nil
}

// DeleteVoiceProfile removes the selected profile from the active Colab
// worker when credentials are available, then destroys its private local
// reference backup and metadata. A reset/expired Colab has no durable remote
// profile, so deleting the local library record is sufficient in that case.
func (a *App) DeleteVoiceProfile(request VoiceProfileDeleteRequest) error {
	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		return errors.New("select a voice profile to delete")
	}
	library, err := a.loadVoiceProfileLibrary()
	if err != nil {
		return err
	}
	index := -1
	for i := range library.Profiles {
		if library.Profiles[i].LibraryID == profileID {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("voice profile is not present in the local KOVA library")
	}
	record := library.Profiles[index]
	workerURL := strings.TrimSpace(request.BaseURL)
	if workerURL == "" {
		workerURL = strings.TrimSpace(record.WorkerURL)
	}
	if workerURL != "" && strings.TrimSpace(request.Token) != "" && strings.TrimSpace(record.RemoteProfileID) != "" {
		workerURL, err = normalizeVoiceURL(workerURL)
		if err != nil {
			return err
		}
		httpRequest, err := http.NewRequest(http.MethodDelete, workerURL+"/v1/profiles/"+url.PathEscape(record.RemoteProfileID), nil)
		if err != nil {
			return err
		}
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(request.Token))
		client := a.httpClient
		if client == nil {
			client = http.DefaultClient
		}
		response, requestErr := client.Do(httpRequest)
		if requestErr != nil {
			return fmt.Errorf("delete profile from Voice Studio: %w", requestErr)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNotFound && (response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices) {
			detail, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
			return fmt.Errorf("Voice Studio delete returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
		}
	}
	if strings.TrimSpace(record.ReferenceFile) != "" {
		path := filepath.Join(a.voiceLibraryDir(), "references", filepath.Base(record.ReferenceFile))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove saved voice reference: %w", err)
		}
	}
	library.Profiles = append(library.Profiles[:index], library.Profiles[index+1:]...)
	return a.writeVoiceProfileLibrary(library)
}
