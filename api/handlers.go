package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/afkarxyz/SpotiFLAC/backend"
)

type Handler struct {
	jobs      *JobManager
	outputDir string
	cancelMap map[string]context.CancelFunc
	drive     *DriveClient
}

func NewHandler(jobs *JobManager, outputDir string, drive *DriveClient) *Handler {
	return &Handler{
		jobs:      jobs,
		outputDir: outputDir,
		cancelMap: make(map[string]context.CancelFunc),
		drive:     drive,
	}
}

// --- Request / Response types ---

type DownloadRequest struct {
	SpotifyURL           string `json:"spotify_url"`
	Service              string `json:"service"`
	AudioFormat          string `json:"audio_format"`
	FilenameFormat       string `json:"filename_format"`
	OutputDir            string `json:"output_dir"`
	Separator            string `json:"separator"`
	MaxConcurrent        int    `json:"max_concurrent"`
	MaxRetries           int    `json:"max_retries"`
	EmbedLyrics          bool   `json:"embed_lyrics"`
	EmbedGenre           bool   `json:"embed_genre"`
	EmbedMaxQualityCover bool   `json:"embed_max_quality_cover"`
	AllowFallback        *bool  `json:"allow_fallback"`
	TrackNumber          bool   `json:"track_number"`
	UseAlbumTrackNumber  bool   `json:"use_album_track_number"`
	UseFirstArtistOnly   bool   `json:"use_first_artist_only"`
	UseSingleGenre       bool   `json:"use_single_genre"`
	UploadToDrive        bool   `json:"upload_to_drive"`
	DriveFolderID        string `json:"drive_folder_id"`
	DeleteAfterUpload    bool   `json:"delete_after_upload"`
}

type MetadataRequest struct {
	SpotifyURL string  `json:"spotify_url"`
	Batch      bool    `json:"batch"`
	Delay      float64 `json:"delay"`
	Separator  string  `json:"separator"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type JobResponse struct {
	JobID   string `json:"job_id"`
	Message string `json:"message"`
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, ErrorResponse{Error: msg})
}

func parseSpotifyType(url string) string {
	for _, t := range []string{"track", "album", "playlist", "artist"} {
		if strings.Contains(url, "/"+t+"/") || strings.Contains(url, ":"+t+":") {
			return t
		}
	}
	return ""
}

func (req *DownloadRequest) allowFallbackValue() bool {
	if req.AllowFallback == nil {
		return true
	}
	return *req.AllowFallback
}

// --- Health ---

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": backend.AppVersion,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Metadata ---

func (h *Handler) GetMetadata(w http.ResponseWriter, r *http.Request) {
	var req MetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SpotifyURL == "" {
		writeError(w, http.StatusBadRequest, "spotify_url is required")
		return
	}
	if req.Separator == "" {
		req.Separator = ", "
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	data, err := backend.GetFilteredSpotifyData(ctx, req.SpotifyURL, req.Batch, time.Duration(req.Delay*float64(time.Second)), req.Separator, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fetch metadata: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, data)
}

// --- Download (batch) ---

func (h *Handler) StartDownload(w http.ResponseWriter, r *http.Request) {
	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SpotifyURL == "" {
		writeError(w, http.StatusBadRequest, "spotify_url is required")
		return
	}

	spotifyType := parseSpotifyType(req.SpotifyURL)
	if spotifyType == "" {
		writeError(w, http.StatusBadRequest, "invalid spotify URL: must contain track, album, playlist, or artist")
		return
	}

	if req.Service == "" {
		req.Service = "tidal"
	}
	if req.AudioFormat == "" {
		req.AudioFormat = "LOSSLESS"
	}
	if req.FilenameFormat == "" {
		req.FilenameFormat = "title-artist"
	}
	if req.Separator == "" {
		req.Separator = ", "
	}
	if req.MaxConcurrent <= 0 {
		req.MaxConcurrent = 3
	}
	if req.MaxConcurrent > 10 {
		req.MaxConcurrent = 10
	}
	if req.MaxRetries <= 0 {
		req.MaxRetries = 3
	}
	if req.MaxRetries > 10 {
		req.MaxRetries = 10
	}

	outDir := req.OutputDir
	if outDir == "" {
		outDir = h.outputDir
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	job := &Job{
		ID:          jobID,
		Status:      JobStatusPending,
		SpotifyURL:  req.SpotifyURL,
		SpotifyType: spotifyType,
		Service:     req.Service,
		AudioFormat: req.AudioFormat,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		OutputDir:   outDir,
	}
	h.jobs.Create(jobID, job)

	ctx, cancel := context.WithCancel(context.Background())
	h.cancelMap[jobID] = cancel

	go h.processDownloadJob(ctx, jobID, req, outDir)

	writeJSON(w, http.StatusAccepted, JobResponse{
		JobID:   jobID,
		Message: "download job created",
	})
}

func (h *Handler) processDownloadJob(ctx context.Context, jobID string, req DownloadRequest, outDir string) {
	defer func() {
		delete(h.cancelMap, jobID)
	}()

	h.jobs.UpdateStatus(jobID, JobStatusFetching)

	metaCtx, metaCancel := context.WithTimeout(ctx, 120*time.Second)
	defer metaCancel()

	data, err := backend.GetFilteredSpotifyData(metaCtx, req.SpotifyURL, true, 0, req.Separator, nil)
	if err != nil {
		h.jobs.SetError(jobID, fmt.Sprintf("metadata fetch failed: %v", err))
		return
	}

	select {
	case <-ctx.Done():
		h.jobs.UpdateStatus(jobID, JobStatusCancelled)
		return
	default:
	}

	tracks := h.extractTracks(data, jobID)
	if len(tracks) == 0 {
		h.jobs.SetError(jobID, "no tracks found")
		return
	}

	h.jobs.SetTracks(jobID, tracks)
	h.jobs.UpdateStatus(jobID, JobStatusDownloading)

	sem := make(chan struct{}, req.MaxConcurrent)
	done := make(chan struct{})

	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i, track := range tracks {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sem <- struct{}{}
			wg.Add(1)
			go func(idx int, t TrackJob) {
				defer func() { <-sem; wg.Done() }()

				select {
				case <-ctx.Done():
					return
				default:
				}

				h.jobs.UpdateTrack(jobID, idx, JobStatusDownloading, "", "", 0)
				h.downloadSingleTrack(ctx, jobID, idx, t, req, outDir)
			}(i, track)
		}
		wg.Wait()
	}()

	<-done

	j := h.jobs.Get(jobID)
	if j != nil && j.Status == JobStatusDownloading {
		h.jobs.UpdateStatus(jobID, JobStatusCompleted)
	}
}

// trackMetaFromSpotify is the metadata struct used for parsing per-track enrichment
type trackMetaFromSpotify struct {
	Copyright   string `json:"copyright"`
	Publisher   string `json:"publisher"`
	TotalDiscs  int    `json:"total_discs"`
	TotalTracks int    `json:"total_tracks"`
	TrackNumber int    `json:"track_number"`
	ReleaseDate string `json:"release_date"`
	CoverURL    string `json:"images"`
}

func (h *Handler) extractTracks(data interface{}, jobID string) []TrackJob {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	var tracks []TrackJob

	// Try single track
	var trackResp struct {
		Track struct {
			SpotifyID   string `json:"spotify_id"`
			Name        string `json:"name"`
			Artists     string `json:"artists"`
			AlbumName   string `json:"album_name"`
			AlbumArtist string `json:"album_artist"`
			DurationMS  int    `json:"duration_ms"`
			Images      string `json:"images"`
			ReleaseDate string `json:"release_date"`
			TrackNumber int    `json:"track_number"`
			TotalTracks int    `json:"total_tracks"`
			DiscNumber  int    `json:"disc_number"`
			TotalDiscs  int    `json:"total_discs"`
		} `json:"track"`
	}
	if json.Unmarshal(jsonBytes, &trackResp) == nil && trackResp.Track.Name != "" {
		h.jobs.SetMetaName(jobID, trackResp.Track.Name)
		t := trackResp.Track
		return []TrackJob{{
			TrackName:   t.Name,
			ArtistName:  t.Artists,
			SpotifyID:   t.SpotifyID,
			AlbumName:   t.AlbumName,
			AlbumArtist: t.AlbumArtist,
			CoverURL:    t.Images,
			ReleaseDate: t.ReleaseDate,
			TrackNumber: t.TrackNumber,
			DiscNumber:  t.DiscNumber,
			TotalTracks: t.TotalTracks,
			TotalDiscs:  t.TotalDiscs,
			DurationMS:  t.DurationMS,
			Status:      JobStatusPending,
		}}
	}

	// Shared struct for track lists
	type rawTrack struct {
		SpotifyID   string `json:"spotify_id"`
		Name        string `json:"name"`
		Artists     string `json:"artists"`
		AlbumName   string `json:"album_name"`
		AlbumArtist string `json:"album_artist"`
		DurationMS  int    `json:"duration_ms"`
		Images      string `json:"images"`
		ReleaseDate string `json:"release_date"`
		TrackNumber int    `json:"track_number"`
		TotalTracks int    `json:"total_tracks"`
		DiscNumber  int    `json:"disc_number"`
		TotalDiscs  int    `json:"total_discs"`
	}
	toTrackJob := func(t rawTrack) TrackJob {
		return TrackJob{
			TrackName:   t.Name,
			ArtistName:  t.Artists,
			SpotifyID:   t.SpotifyID,
			AlbumName:   t.AlbumName,
			AlbumArtist: t.AlbumArtist,
			CoverURL:    t.Images,
			ReleaseDate: t.ReleaseDate,
			TrackNumber: t.TrackNumber,
			DiscNumber:  t.DiscNumber,
			TotalTracks: t.TotalTracks,
			TotalDiscs:  t.TotalDiscs,
			DurationMS:  t.DurationMS,
			Status:      JobStatusPending,
		}
	}

	// Try album
	var albumResp struct {
		AlbumInfo struct{ Name string `json:"name"` } `json:"album_info"`
		TrackList []rawTrack                           `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &albumResp) == nil && len(albumResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, albumResp.AlbumInfo.Name)
		for _, t := range albumResp.TrackList {
			tracks = append(tracks, toTrackJob(t))
		}
		return tracks
	}

	// Try playlist
	var playlistResp struct {
		PlaylistInfo struct{ Name string `json:"name"` } `json:"playlist_info"`
		TrackList    []rawTrack                           `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &playlistResp) == nil && len(playlistResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, playlistResp.PlaylistInfo.Name)
		for _, t := range playlistResp.TrackList {
			tracks = append(tracks, toTrackJob(t))
		}
		return tracks
	}

	// Try artist
	var artistResp struct {
		ArtistInfo struct{ Name string `json:"name"` } `json:"artist_info"`
		TrackList  []rawTrack                           `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &artistResp) == nil && len(artistResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, artistResp.ArtistInfo.Name)
		for _, t := range artistResp.TrackList {
			tracks = append(tracks, toTrackJob(t))
		}
		return tracks
	}

	return nil
}

// downloadSingleTrack mirrors the logic in app.go DownloadTrack:
// 1. Enrich metadata from Spotify (copyright, publisher, etc.)
// 2. Check if file already exists
// 3. Start lyrics fetch concurrently (if enabled)
// 4. Start ISRC lookup concurrently (if qobuz)
// 5. Download with retry + fallback
// 6. Handle EXISTS: prefix from downloader
// 7. Validate duration
// 8. Clean up partial/corrupted files on failure
// 9. Embed lyrics
// 10. Upload to Drive
func (h *Handler) downloadSingleTrack(ctx context.Context, jobID string, idx int, track TrackJob, req DownloadRequest, outDir string) {
	select {
	case <-ctx.Done():
		h.jobs.UpdateTrack(jobID, idx, JobStatusCancelled, "cancelled", "", 0)
		return
	default:
	}

	// --- Step 1: Enrich metadata from Spotify (same as app.go:315-370) ---
	copyright := ""
	publisher := ""
	releaseDate := track.ReleaseDate
	trackNumber := track.TrackNumber
	discNumber := track.DiscNumber
	totalTracks := track.TotalTracks
	totalDiscs := track.TotalDiscs
	coverURL := track.CoverURL

	if track.SpotifyID != "" && (releaseDate == "" || totalTracks == 0 || trackNumber == 0 || totalDiscs == 0) {
		enrichCtx, enrichCancel := context.WithTimeout(ctx, 10*time.Second)
		trackURL := fmt.Sprintf("https://open.spotify.com/track/%s", track.SpotifyID)
		trackData, err := backend.GetFilteredSpotifyData(enrichCtx, trackURL, false, 0, req.Separator, nil)
		enrichCancel()
		if err == nil {
			var resp struct {
				Track trackMetaFromSpotify `json:"track"`
			}
			if jsonData, jsonErr := json.Marshal(trackData); jsonErr == nil {
				if json.Unmarshal(jsonData, &resp) == nil {
					if copyright == "" && resp.Track.Copyright != "" {
						copyright = resp.Track.Copyright
					}
					if publisher == "" && resp.Track.Publisher != "" {
						publisher = resp.Track.Publisher
					}
					if totalDiscs == 0 && resp.Track.TotalDiscs > 0 {
						totalDiscs = resp.Track.TotalDiscs
					}
					if totalTracks == 0 && resp.Track.TotalTracks > 0 {
						totalTracks = resp.Track.TotalTracks
					}
					if trackNumber == 0 && resp.Track.TrackNumber > 0 {
						trackNumber = resp.Track.TrackNumber
					}
					if releaseDate == "" && resp.Track.ReleaseDate != "" {
						releaseDate = resp.Track.ReleaseDate
					}
					if coverURL == "" && resp.Track.CoverURL != "" {
						coverURL = resp.Track.CoverURL
					}
				}
			}
		}
	}

	spotifyURL := ""
	if track.SpotifyID != "" {
		spotifyURL = fmt.Sprintf("https://open.spotify.com/track/%s", track.SpotifyID)
	}

	downloadOutDir := outDir
	if err := os.MkdirAll(downloadOutDir, 0755); err != nil {
		h.jobs.UpdateTrack(jobID, idx, JobStatusFailed, fmt.Sprintf("mkdir failed: %v", err), "", 0)
		return
	}

	// --- Step 2: Check if file already exists (same as app.go:373-388) ---
	if track.TrackName != "" && track.ArtistName != "" {
		expectedFilename := backend.BuildExpectedFilename(
			track.TrackName, track.ArtistName, track.AlbumName, track.AlbumArtist,
			releaseDate, req.FilenameFormat, "", "",
			req.TrackNumber, idx+1, discNumber, req.UseAlbumTrackNumber,
		)
		expectedPath := filepath.Join(downloadOutDir, expectedFilename)
		if info, err := os.Stat(expectedPath); err == nil && info.Size() > 100*1024 {
			h.jobs.UpdateTrack(jobID, idx, JobStatusCompleted, "", expectedPath, info.Size())
			return
		}
	}

	// --- Step 3: Start lyrics fetch concurrently (same as app.go:390-407) ---
	lyricsChan := make(chan string, 1)
	if req.EmbedLyrics && track.SpotifyID != "" {
		go func() {
			client := backend.NewLyricsClient()
			resp, _, err := client.FetchLyricsAllSources(track.SpotifyID, track.TrackName, track.ArtistName, track.AlbumName, track.DurationMS)
			if err == nil && resp != nil {
				lrc := client.ConvertToLRC(resp, track.TrackName, track.ArtistName)
				lyricsChan <- lrc
			} else {
				lyricsChan <- ""
			}
		}()
	} else {
		close(lyricsChan)
	}

	// --- Step 4: Start ISRC lookup concurrently for Qobuz (same as app.go:409-429) ---
	isrcChan := make(chan string, 1)
	if track.SpotifyID != "" && req.Service == "qobuz" {
		go func() {
			client := backend.NewSongLinkClient()
			isrc, err := client.GetISRCDirect(track.SpotifyID)
			if err != nil {
				log.Printf("ISRC lookup failed for %s: %v", track.SpotifyID, err)
			}
			isrcChan <- isrc
		}()
	} else {
		close(isrcChan)
	}

	// --- Step 5: Download with retry + service fallback rotation ---
	// Build service order: requested service first, then rotate through others.
	// Each service gets req.MaxRetries attempts before falling back to the next.
	serviceOrder := buildServiceOrder(req.Service)

	var downloadErr error
	var filename string
	succeeded := false

	for _, svc := range serviceOrder {
		if succeeded {
			break
		}
		select {
		case <-ctx.Done():
			h.jobs.UpdateTrack(jobID, idx, JobStatusCancelled, "cancelled", "", 0)
			return
		default:
		}

		for attempt := 0; attempt < req.MaxRetries; attempt++ {
			select {
			case <-ctx.Done():
				h.jobs.UpdateTrack(jobID, idx, JobStatusCancelled, "cancelled", "", 0)
				return
			default:
			}

			filename, downloadErr = h.tryDownloadService(svc, track, req, downloadOutDir, idx, spotifyURL, coverURL, releaseDate, copyright, publisher, trackNumber, discNumber, totalTracks, totalDiscs, isrcChan)

			if downloadErr == nil {
				succeeded = true
				break
			}

			// Clean up partial/corrupted file on failure
			if filename != "" && !strings.HasPrefix(filename, "EXISTS:") {
				if _, statErr := os.Stat(filename); statErr == nil {
					log.Printf("Removing partial file after failed download: %s", filename)
					os.Remove(filename)
				}
			}

			if attempt < req.MaxRetries-1 {
				log.Printf("[%s] attempt %d/%d failed for %q: %v, retrying...", svc, attempt+1, req.MaxRetries, track.TrackName, downloadErr)
				time.Sleep(time.Duration(attempt+1) * 2 * time.Second)

				// Re-enqueue ISRC for qobuz retry
				if svc == "qobuz" && track.SpotifyID != "" {
					isrcChan = make(chan string, 1)
					go func() {
						client := backend.NewSongLinkClient()
						isrc, _ := client.GetISRCDirect(track.SpotifyID)
						isrcChan <- isrc
					}()
				}
			}
		}

		if !succeeded {
			log.Printf("[%s] all %d attempts failed for %q, trying next service...", svc, req.MaxRetries, track.TrackName)

			// Prepare ISRC channel if next service might be qobuz
			if track.SpotifyID != "" {
				isrcChan = make(chan string, 1)
				go func() {
					client := backend.NewSongLinkClient()
					isrc, _ := client.GetISRCDirect(track.SpotifyID)
					isrcChan <- isrc
				}()
			}
		}
	}

	if !succeeded {
		h.jobs.UpdateTrack(jobID, idx, JobStatusFailed, fmt.Sprintf("all services failed: %v", downloadErr), "", 0)
		return
	}

	// --- Step 6: Handle EXISTS: prefix (same as app.go:500-504) ---
	alreadyExists := false
	if strings.HasPrefix(filename, "EXISTS:") {
		alreadyExists = true
		filename = strings.TrimPrefix(filename, "EXISTS:")
	}

	// --- Step 7: Validate duration (same as app.go:506-521) ---
	// DurationMS is in milliseconds, ValidateDownloadedTrackDuration expects seconds
	if !alreadyExists && track.DurationMS > 0 {
		durationSec := track.DurationMS / 1000
		validated, validationErr := backend.ValidateDownloadedTrackDuration(filename, durationSec)
		if validationErr != nil {
			os.Remove(filename)
			h.jobs.UpdateTrack(jobID, idx, JobStatusFailed, validationErr.Error(), "", 0)
			return
		}
		if !validated {
			log.Printf("Skipped duration validation for %s (expected=%dms)", filename, track.DurationMS)
		}
	}

	// --- Step 8: Embed lyrics (same as app.go:523-547) ---
	if !alreadyExists && req.EmbedLyrics && track.SpotifyID != "" &&
		(strings.HasSuffix(filename, ".flac") || strings.HasSuffix(filename, ".mp3") || strings.HasSuffix(filename, ".m4a")) {
		lyrics := <-lyricsChan
		if lyrics != "" {
			if err := backend.EmbedLyricsOnlyUniversal(filename, lyrics); err != nil {
				log.Printf("Failed to embed lyrics for %q: %v", track.TrackName, err)
			}
		}
	} else {
		// Drain the channel
		select {
		case <-lyricsChan:
		default:
		}
	}

	// --- Final: update job status ---
	var fileSize int64
	if info, err := os.Stat(filename); err == nil {
		fileSize = info.Size()
	}
	h.jobs.UpdateTrack(jobID, idx, JobStatusCompleted, "", filename, fileSize)

	// --- Step 9: Upload to Google Drive ---
	if req.UploadToDrive && h.drive != nil && filename != "" {
		jobInfo := h.jobs.Get(jobID)
		folderName := ""
		if jobInfo != nil {
			folderName = jobInfo.MetaName
		}

		driveCtx, driveCancel := context.WithTimeout(ctx, 5*time.Minute)
		defer driveCancel()

		var driveFileID, driveLink string
		var driveErr error
		if req.DriveFolderID != "" {
			driveFileID, driveLink, driveErr = h.drive.Upload(driveCtx, filename, req.DriveFolderID)
		} else {
			driveFileID, driveLink, driveErr = h.drive.UploadWithJobFolder(driveCtx, filename, folderName)
		}

		if driveErr != nil {
			log.Printf("Drive upload failed for %q: %v", track.TrackName, driveErr)
		} else {
			h.jobs.SetTrackDriveInfo(jobID, idx, driveFileID, driveLink)
			log.Printf("Uploaded %q to Drive: %s", track.TrackName, driveLink)

			if req.DeleteAfterUpload {
				if err := os.Remove(filename); err != nil {
					log.Printf("Warning: failed to delete local file after drive upload: %v", err)
				}
			}
		}
	}
}

// --- Job status endpoints ---

func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := h.jobs.List()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID := extractPathParam(r.URL.Path, "/api/v1/jobs/")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job_id is required")
		return
	}
	if idx := strings.Index(jobID, "/"); idx != -1 {
		jobID = jobID[:idx]
	}

	job := h.jobs.Get(jobID)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	jobID := strings.TrimSuffix(path, "/cancel")

	if cancel, ok := h.cancelMap[jobID]; ok {
		cancel()
	}

	if h.jobs.Cancel(jobID) {
		writeJSON(w, http.StatusOK, map[string]string{"message": "job cancelled"})
	} else {
		writeError(w, http.StatusNotFound, "job not found or already completed")
	}
}

func (h *Handler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID := extractPathParam(r.URL.Path, "/api/v1/jobs/")
	if h.jobs.Delete(jobID) {
		writeJSON(w, http.StatusOK, map[string]string{"message": "job deleted"})
	} else {
		writeError(w, http.StatusNotFound, "job not found")
	}
}

// --- Service availability ---

func (h *Handler) CheckAvailability(w http.ResponseWriter, r *http.Request) {
	spotifyID := r.URL.Query().Get("spotify_id")
	if spotifyID == "" {
		writeError(w, http.StatusBadRequest, "spotify_id query param is required")
		return
	}

	client := backend.NewSongLinkClient()
	avail, err := client.CheckTrackAvailability(spotifyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("availability check failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, avail)
}

// --- Cleanup ---

func (h *Handler) CleanupJobs(w http.ResponseWriter, r *http.Request) {
	removed := h.jobs.Cleanup(24 * time.Hour)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"removed": removed,
		"message": "cleaned up completed jobs older than 24 hours",
	})
}

// buildServiceOrder returns the services to try, starting with the requested one.
func buildServiceOrder(preferred string) []string {
	all := []string{"tidal", "qobuz", "amazon"}
	if preferred == "" {
		return all
	}
	order := []string{preferred}
	for _, s := range all {
		if s != preferred {
			order = append(order, s)
		}
	}
	return order
}

func (h *Handler) tryDownloadService(
	svc string,
	track TrackJob, req DownloadRequest, outDir string, idx int,
	spotifyURL, coverURL, releaseDate, copyright, publisher string,
	trackNumber, discNumber, totalTracks, totalDiscs int,
	isrcChan chan string,
) (string, error) {
	if track.SpotifyID == "" {
		return "", fmt.Errorf("no spotify ID")
	}

	switch svc {
	case "tidal":
		dl := backend.NewTidalDownloader("")
		return dl.Download(
			track.SpotifyID, outDir, req.AudioFormat, req.FilenameFormat,
			req.TrackNumber, idx+1,
			track.TrackName, track.ArtistName, track.AlbumName, track.AlbumArtist, releaseDate,
			req.UseAlbumTrackNumber, coverURL, req.EmbedMaxQualityCover,
			trackNumber, discNumber, totalTracks, totalDiscs,
			copyright, publisher, spotifyURL,
			req.allowFallbackValue(), req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre,
		)

	case "qobuz":
		isrc := <-isrcChan
		if isrc == "" {
			return "", fmt.Errorf("ISRC lookup failed for qobuz")
		}
		quality := req.AudioFormat
		if quality == "" || quality == "LOSSLESS" {
			quality = "6"
		} else if quality == "HI_RES_LOSSLESS" {
			quality = "27"
		}
		dl := backend.NewQobuzDownloader()
		return dl.DownloadTrackWithISRC(
			isrc, track.SpotifyID, outDir, quality, req.FilenameFormat,
			req.TrackNumber, idx+1,
			track.TrackName, track.ArtistName, track.AlbumName, track.AlbumArtist, releaseDate,
			req.UseAlbumTrackNumber, coverURL, req.EmbedMaxQualityCover,
			trackNumber, discNumber, totalTracks, totalDiscs,
			copyright, publisher, spotifyURL,
			req.allowFallbackValue(), req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre,
		)

	case "amazon":
		dl := backend.NewAmazonDownloader()
		return dl.DownloadBySpotifyID(
			track.SpotifyID, outDir, req.AudioFormat, req.FilenameFormat,
			"", "", // playlistName, playlistOwner
			req.TrackNumber, idx+1,
			track.TrackName, track.ArtistName, track.AlbumName, track.AlbumArtist, releaseDate,
			coverURL, trackNumber, discNumber, totalTracks,
			req.EmbedMaxQualityCover, totalDiscs,
			copyright, publisher, spotifyURL,
			req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre,
		)

	default:
		return "", fmt.Errorf("unknown service: %s", svc)
	}
}

func extractPathParam(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	return s
}
