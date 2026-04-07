package api

import (
	"sync"
	"time"
)

type JobStatus string

const (
	JobStatusPending     JobStatus = "pending"
	JobStatusFetching    JobStatus = "fetching_metadata"
	JobStatusDownloading JobStatus = "downloading"
	JobStatusCompleted   JobStatus = "completed"
	JobStatusFailed      JobStatus = "failed"
	JobStatusCancelled   JobStatus = "cancelled"
)

type TrackJob struct {
	TrackName  string    `json:"track_name"`
	ArtistName string    `json:"artist_name"`
	SpotifyID  string    `json:"spotify_id"`
	Status     JobStatus `json:"status"`
	Error      string    `json:"error,omitempty"`
	FilePath   string    `json:"file_path,omitempty"`
	FileSize   int64     `json:"file_size,omitempty"`
}

type Job struct {
	ID          string     `json:"id"`
	Status      JobStatus  `json:"status"`
	SpotifyURL  string     `json:"spotify_url"`
	SpotifyType string     `json:"spotify_type"`
	Service     string     `json:"service"`
	AudioFormat string     `json:"audio_format"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	TotalTracks int        `json:"total_tracks"`
	Completed   int        `json:"completed"`
	Failed      int        `json:"failed"`
	Skipped     int        `json:"skipped"`
	Tracks      []TrackJob `json:"tracks,omitempty"`
	OutputDir   string     `json:"output_dir"`
	MetaName    string     `json:"meta_name,omitempty"`
}

type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*Job),
	}
}

func (jm *JobManager) Create(id string, job *Job) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	jm.jobs[id] = job
}

func (jm *JobManager) Get(id string) *Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	j, ok := jm.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	tracks := make([]TrackJob, len(j.Tracks))
	copy(tracks, j.Tracks)
	cp.Tracks = tracks
	return &cp
}

func (jm *JobManager) List() []*Job {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	list := make([]*Job, 0, len(jm.jobs))
	for _, j := range jm.jobs {
		cp := *j
		cp.Tracks = nil // summary only
		list = append(list, &cp)
	}
	return list
}

func (jm *JobManager) UpdateStatus(id string, status JobStatus) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if j, ok := jm.jobs[id]; ok {
		j.Status = status
		j.UpdatedAt = time.Now()
		if status == JobStatusCompleted || status == JobStatusFailed || status == JobStatusCancelled {
			now := time.Now()
			j.CompletedAt = &now
		}
	}
}

func (jm *JobManager) SetError(id string, errMsg string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if j, ok := jm.jobs[id]; ok {
		j.Error = errMsg
		j.Status = JobStatusFailed
		j.UpdatedAt = time.Now()
		now := time.Now()
		j.CompletedAt = &now
	}
}

func (jm *JobManager) SetTracks(id string, tracks []TrackJob) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if j, ok := jm.jobs[id]; ok {
		j.Tracks = tracks
		j.TotalTracks = len(tracks)
		j.UpdatedAt = time.Now()
	}
}

func (jm *JobManager) SetMetaName(id string, name string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if j, ok := jm.jobs[id]; ok {
		j.MetaName = name
		j.UpdatedAt = time.Now()
	}
}

func (jm *JobManager) UpdateTrack(id string, idx int, status JobStatus, errMsg string, filePath string, fileSize int64) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	j, ok := jm.jobs[id]
	if !ok || idx < 0 || idx >= len(j.Tracks) {
		return
	}
	prev := j.Tracks[idx].Status
	j.Tracks[idx].Status = status
	j.Tracks[idx].Error = errMsg
	j.Tracks[idx].FilePath = filePath
	j.Tracks[idx].FileSize = fileSize
	j.UpdatedAt = time.Now()

	// Update counters (undo previous, apply new)
	jm.adjustCounters(j, prev, -1)
	jm.adjustCounters(j, status, 1)
}

func (jm *JobManager) adjustCounters(j *Job, status JobStatus, delta int) {
	switch status {
	case JobStatusCompleted:
		j.Completed += delta
	case JobStatusFailed:
		j.Failed += delta
	}
}

func (jm *JobManager) Cancel(id string) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	j, ok := jm.jobs[id]
	if !ok {
		return false
	}
	if j.Status == JobStatusCompleted || j.Status == JobStatusCancelled {
		return false
	}
	j.Status = JobStatusCancelled
	j.UpdatedAt = time.Now()
	now := time.Now()
	j.CompletedAt = &now
	return true
}

func (jm *JobManager) Delete(id string) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if _, ok := jm.jobs[id]; ok {
		delete(jm.jobs, id)
		return true
	}
	return false
}

func (jm *JobManager) Cleanup(olderThan time.Duration) int {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for id, j := range jm.jobs {
		if j.CompletedAt != nil && j.CompletedAt.Before(cutoff) {
			delete(jm.jobs, id)
			removed++
		}
	}
	return removed
}
