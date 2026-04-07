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
		return true // default to true
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

	// Apply defaults
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

	// Concurrent download with semaphore
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

func (h *Handler) extractTracks(data interface{}, jobID string) []TrackJob {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	var tracks []TrackJob

	// Try single track
	var trackResp struct {
		Track struct {
			SpotifyID string `json:"spotify_id"`
			Name      string `json:"name"`
			Artists   string `json:"artists"`
			AlbumName string `json:"album_name"`
		} `json:"track"`
	}
	if json.Unmarshal(jsonBytes, &trackResp) == nil && trackResp.Track.Name != "" {
		h.jobs.SetMetaName(jobID, trackResp.Track.Name)
		return []TrackJob{{
			TrackName:  trackResp.Track.Name,
			ArtistName: trackResp.Track.Artists,
			SpotifyID:  trackResp.Track.SpotifyID,
			Status:     JobStatusPending,
		}}
	}

	// Try album
	var albumResp struct {
		AlbumInfo struct {
			Name string `json:"name"`
		} `json:"album_info"`
		TrackList []struct {
			SpotifyID string `json:"spotify_id"`
			Name      string `json:"name"`
			Artists   string `json:"artists"`
		} `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &albumResp) == nil && len(albumResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, albumResp.AlbumInfo.Name)
		for _, t := range albumResp.TrackList {
			tracks = append(tracks, TrackJob{
				TrackName:  t.Name,
				ArtistName: t.Artists,
				SpotifyID:  t.SpotifyID,
				Status:     JobStatusPending,
			})
		}
		return tracks
	}

	// Try playlist
	var playlistResp struct {
		PlaylistInfo struct {
			Name string `json:"name"`
		} `json:"playlist_info"`
		TrackList []struct {
			SpotifyID string `json:"spotify_id"`
			Name      string `json:"name"`
			Artists   string `json:"artists"`
		} `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &playlistResp) == nil && len(playlistResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, playlistResp.PlaylistInfo.Name)
		for _, t := range playlistResp.TrackList {
			tracks = append(tracks, TrackJob{
				TrackName:  t.Name,
				ArtistName: t.Artists,
				SpotifyID:  t.SpotifyID,
				Status:     JobStatusPending,
			})
		}
		return tracks
	}

	// Try artist
	var artistResp struct {
		ArtistInfo struct {
			Name string `json:"name"`
		} `json:"artist_info"`
		TrackList []struct {
			SpotifyID string `json:"spotify_id"`
			Name      string `json:"name"`
			Artists   string `json:"artists"`
		} `json:"track_list"`
	}
	if json.Unmarshal(jsonBytes, &artistResp) == nil && len(artistResp.TrackList) > 0 {
		h.jobs.SetMetaName(jobID, artistResp.ArtistInfo.Name)
		for _, t := range artistResp.TrackList {
			tracks = append(tracks, TrackJob{
				TrackName:  t.Name,
				ArtistName: t.Artists,
				SpotifyID:  t.SpotifyID,
				Status:     JobStatusPending,
			})
		}
		return tracks
	}

	return nil
}

func (h *Handler) downloadSingleTrack(ctx context.Context, jobID string, idx int, track TrackJob, req DownloadRequest, outDir string) {
	select {
	case <-ctx.Done():
		h.jobs.UpdateTrack(jobID, idx, JobStatusCancelled, "cancelled", "", 0)
		return
	default:
	}

	downloadOutDir := outDir

	// Build filename to check if already exists
	filename := backend.BuildExpectedFilename(
		track.TrackName, track.ArtistName,
		"", "", "", // album, albumArtist, releaseDate
		req.FilenameFormat,
		"", "",            // playlistName, playlistOwner
		req.TrackNumber,   // includeTrackNumber
		idx+1,             // position
		0,                 // discNumber
		req.UseAlbumTrackNumber,
	)

	destPath := filepath.Join(downloadOutDir, filename)

	// Check if already exists
	if info, err := os.Stat(destPath); err == nil && info.Size() > 100*1024 {
		h.jobs.UpdateTrack(jobID, idx, JobStatusCompleted, "", destPath, info.Size())
		return
	}

	// Ensure output directory exists
	if err := os.MkdirAll(downloadOutDir, 0755); err != nil {
		h.jobs.UpdateTrack(jobID, idx, JobStatusFailed, fmt.Sprintf("mkdir failed: %v", err), "", 0)
		return
	}

	// Retry loop
	var downloadErr error
	var resultPath string
	for attempt := 0; attempt < req.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			h.jobs.UpdateTrack(jobID, idx, JobStatusCancelled, "cancelled", "", 0)
			return
		default:
		}

		switch req.Service {
		case "tidal":
			resultPath, downloadErr = h.downloadViaTidal(track, req, downloadOutDir, idx)
		case "qobuz":
			resultPath, downloadErr = h.downloadViaQobuz(track, req, downloadOutDir, idx)
		default:
			resultPath, downloadErr = h.downloadViaTidal(track, req, downloadOutDir, idx)
		}

		if downloadErr == nil {
			break
		}

		if attempt < req.MaxRetries-1 {
			log.Printf("Download attempt %d/%d failed for %q: %v, retrying...", attempt+1, req.MaxRetries, track.TrackName, downloadErr)
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
	}

	if downloadErr != nil {
		h.jobs.UpdateTrack(jobID, idx, JobStatusFailed, downloadErr.Error(), "", 0)
		return
	}

	// Embed lyrics if enabled
	if req.EmbedLyrics && track.SpotifyID != "" && resultPath != "" {
		h.embedLyrics(track, resultPath)
	}

	var fileSize int64
	if info, err := os.Stat(resultPath); err == nil {
		fileSize = info.Size()
	}

	h.jobs.UpdateTrack(jobID, idx, JobStatusCompleted, "", resultPath, fileSize)

	// Upload to Google Drive if enabled
	if req.UploadToDrive && h.drive != nil && resultPath != "" {
		jobInfo := h.jobs.Get(jobID)
		folderName := ""
		if jobInfo != nil {
			folderName = jobInfo.MetaName
		}

		driveCtx, driveCancel := context.WithTimeout(ctx, 5*time.Minute)
		defer driveCancel()

		// Use per-request folder ID, or fall back to job folder name
		var driveFileID, driveLink string
		var driveErr error
		if req.DriveFolderID != "" {
			driveFileID, driveLink, driveErr = h.drive.Upload(driveCtx, resultPath, req.DriveFolderID)
		} else {
			driveFileID, driveLink, driveErr = h.drive.UploadWithJobFolder(driveCtx, resultPath, folderName)
		}

		if driveErr != nil {
			log.Printf("Drive upload failed for %q: %v", track.TrackName, driveErr)
		} else {
			h.jobs.SetTrackDriveInfo(jobID, idx, driveFileID, driveLink)
			log.Printf("Uploaded %q to Drive: %s", track.TrackName, driveLink)

			// Delete local file after upload if requested
			if req.DeleteAfterUpload {
				if err := os.Remove(resultPath); err != nil {
					log.Printf("Warning: failed to delete local file after drive upload: %v", err)
				}
			}
		}
	}
}

func (h *Handler) embedLyrics(track TrackJob, filePath string) {
	lyricsClient := backend.NewLyricsClient()
	lyrics, _, err := lyricsClient.FetchLyricsAllSources(track.SpotifyID, track.TrackName, track.ArtistName, "", 0)
	if err != nil || lyrics == nil {
		return
	}
	lrc := lyricsClient.ConvertToLRC(lyrics, track.TrackName, track.ArtistName)
	if lrc == "" {
		return
	}
	if err := backend.EmbedLyricsOnlyUniversal(filePath, lrc); err != nil {
		log.Printf("Failed to embed lyrics for %q: %v", track.TrackName, err)
	}
}

func (h *Handler) downloadViaTidal(track TrackJob, req DownloadRequest, outDir string, position int) (string, error) {
	if track.SpotifyID == "" {
		return "", fmt.Errorf("no spotify ID for tidal download")
	}
	spotifyURL := fmt.Sprintf("https://open.spotify.com/track/%s", track.SpotifyID)
	dl := backend.NewTidalDownloader("")
	return dl.Download(
		track.SpotifyID, outDir, req.AudioFormat, req.FilenameFormat,
		req.TrackNumber, position,
		track.TrackName, track.ArtistName, "", "", "", // album, albumArtist, releaseDate
		req.UseAlbumTrackNumber,
		"",                       // coverURL
		req.EmbedMaxQualityCover, // embedMaxQualityCover
		0, 0, 0, 0,              // trackNumber, discNumber, totalTracks, totalDiscs
		"", "", spotifyURL,       // copyright, publisher, spotifyURL
		req.allowFallbackValue(), // allowFallback
		req.UseFirstArtistOnly,   // useFirstArtistOnly
		req.UseSingleGenre,       // useSingleGenre
		req.EmbedGenre,           // embedGenre
	)
}

func (h *Handler) downloadViaQobuz(track TrackJob, req DownloadRequest, outDir string, position int) (string, error) {
	if track.SpotifyID == "" {
		return "", fmt.Errorf("spotify ID is required for qobuz")
	}

	slClient := backend.NewSongLinkClient()
	isrc, err := slClient.GetISRCDirect(track.SpotifyID)
	if err != nil || isrc == "" {
		return "", fmt.Errorf("ISRC lookup failed: %v", err)
	}

	quality := "6" // FLAC 16-bit
	if req.AudioFormat == "HI_RES_LOSSLESS" {
		quality = "27"
	}

	spotifyURL := fmt.Sprintf("https://open.spotify.com/track/%s", track.SpotifyID)
	dl := backend.NewQobuzDownloader()
	return dl.DownloadTrackWithISRC(
		isrc, track.SpotifyID, outDir, quality, req.FilenameFormat,
		req.TrackNumber, position,
		track.TrackName, track.ArtistName, "", "", "", // album, albumArtist, releaseDate
		req.UseAlbumTrackNumber,
		"",                       // coverURL
		req.EmbedMaxQualityCover, // embedMaxQualityCover
		0, 0, 0, 0,              // trackNumber, discNumber, totalTracks, totalDiscs
		"", "", spotifyURL,       // copyright, publisher, spotifyURL
		req.allowFallbackValue(), // allowFallback
		req.UseFirstArtistOnly,   // useFirstArtistOnly
		req.UseSingleGenre,       // useSingleGenre
		req.EmbedGenre,           // embedGenre
	)
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

	// Strip trailing sub-paths like /cancel
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

func extractPathParam(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	return s
}
