# SpotiFLAC API

Self-hosted REST API for downloading music from Spotify via Tidal/Qobuz with optional Google Drive upload.

## Quick Start

### Docker Compose

```bash
# Clone and configure
cp .env.example .env  # edit with your settings

# Run
docker compose up -d
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `OUTPUT_DIR` | `/downloads` | Download output directory |
| `API_KEY` | *(none)* | API key for authentication (optional) |
| `GDRIVE_ENABLED` | `false` | Enable Google Drive upload |
| `GDRIVE_CREDENTIALS_FILE` | `./service-account.json` | Path to service account JSON key |
| `GDRIVE_ROOT_FOLDER_ID` | *(none)* | Root Drive folder ID for uploads |
| `GDRIVE_DELETE_AFTER_UPLOAD` | `false` | Delete local files after Drive upload |

### Docker Compose Example

```yaml
services:
  spotiflac-api:
    build: .
    container_name: spotiflac-api
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./downloads:/downloads
      - ./service-account.json:/config/service-account.json:ro
    environment:
      - PORT=8080
      - OUTPUT_DIR=/downloads
      - API_KEY=your-secret-key
      - GDRIVE_ENABLED=true
      - GDRIVE_CREDENTIALS_FILE=/config/service-account.json
      - GDRIVE_ROOT_FOLDER_ID=your-folder-id
      - GDRIVE_DELETE_AFTER_UPLOAD=true
```

## Authentication

If `API_KEY` is set, all requests must include it via header or query parameter:

```bash
# Header
curl -H "X-API-Key: your-secret-key" http://localhost:8080/api/v1/health

# Query parameter
curl http://localhost:8080/api/v1/health?api_key=your-secret-key
```

## API Endpoints

### Health Check

```
GET /health
GET /api/v1/health
```

**Response:**

```json
{
  "status": "ok",
  "version": "7.1.2",
  "time": "2026-04-07T12:00:00Z"
}
```

---

### Fetch Metadata

Fetch Spotify metadata for a track, album, playlist, or artist.

```
POST /api/v1/metadata
```

**Request Body:**

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spotify_url` | string | Yes | | Spotify URL or URI |
| `batch` | bool | No | `false` | Batch mode for large playlists |
| `delay` | float | No | `0` | Delay between requests (seconds) |
| `separator` | string | No | `", "` | Artist name separator |

**Example:**

```bash
curl -X POST http://localhost:8080/api/v1/metadata \
  -H "Content-Type: application/json" \
  -d '{
    "spotify_url": "https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy"
  }'
```

**Response:** Returns track/album/playlist/artist metadata depending on URL type.

---

### Start Download

Start an async download job. Supports single tracks, albums, playlists, and artist discographies.

```
POST /api/v1/download
```

**Request Body:**

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spotify_url` | string | Yes | | Spotify URL (track/album/playlist/artist) |
| `service` | string | No | `"tidal"` | Download service: `"tidal"` or `"qobuz"` |
| `audio_format` | string | No | `"LOSSLESS"` | Quality: `"LOSSLESS"`, `"HI_RES_LOSSLESS"` |
| `filename_format` | string | No | `"title-artist"` | Filename format (see below) |
| `output_dir` | string | No | `$OUTPUT_DIR` | Download destination |
| `separator` | string | No | `", "` | Artist name separator |
| `max_concurrent` | int | No | `3` | Parallel downloads (1-10) |
| `max_retries` | int | No | `3` | Retry attempts per track (1-10) |
| `embed_lyrics` | bool | No | `false` | Fetch and embed synced lyrics |
| `embed_genre` | bool | No | `false` | Embed genre metadata |
| `embed_max_quality_cover` | bool | No | `false` | Embed highest quality cover art |
| `allow_fallback` | bool | No | `true` | Fall back to other APIs on failure |
| `track_number` | bool | No | `false` | Include track number in filename |
| `use_album_track_number` | bool | No | `false` | Use album's track numbering |
| `use_first_artist_only` | bool | No | `false` | Only use primary artist |
| `use_single_genre` | bool | No | `false` | Use single genre tag |
| `upload_to_drive` | bool | No | `false` | Upload to Google Drive after download |
| `drive_folder_id` | string | No | *(auto)* | Target Drive folder ID |
| `delete_after_upload` | bool | No | `false` | Delete local file after Drive upload |

**Filename Format Options:**

| Preset | Output |
|--------|--------|
| `title-artist` | `Track Name - Artist Name.flac` |
| `artist-title` | `Artist Name - Track Name.flac` |
| `title` | `Track Name.flac` |

Or use a custom template: `{title} - {artist} [{album}]`

Available template variables: `{title}`, `{artist}`, `{album}`, `{album_artist}`, `{year}`, `{date}`, `{track}`, `{disc}`, `{playlist}`, `{creator}`

**Example - Download Album:**

```bash
curl -X POST http://localhost:8080/api/v1/download \
  -H "Content-Type: application/json" \
  -d '{
    "spotify_url": "https://open.spotify.com/album/4aawyAB9vmqN3uQ7FjRGTy",
    "service": "tidal",
    "audio_format": "LOSSLESS",
    "embed_lyrics": true,
    "embed_genre": true,
    "max_concurrent": 5
  }'
```

**Example - Download Playlist with Drive Upload:**

```bash
curl -X POST http://localhost:8080/api/v1/download \
  -H "Content-Type: application/json" \
  -d '{
    "spotify_url": "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
    "upload_to_drive": true,
    "delete_after_upload": true,
    "embed_lyrics": true,
    "max_concurrent": 3,
    "max_retries": 5
  }'
```

**Response:**

```json
{
  "job_id": "job-1712476800000000000",
  "message": "download job created"
}
```

---

### List Jobs

```
GET /api/v1/jobs
```

**Response:**

```json
{
  "jobs": [
    {
      "id": "job-1712476800000000000",
      "status": "downloading",
      "spotify_url": "https://open.spotify.com/album/...",
      "spotify_type": "album",
      "service": "tidal",
      "audio_format": "LOSSLESS",
      "created_at": "2026-04-07T12:00:00Z",
      "updated_at": "2026-04-07T12:01:30Z",
      "total_tracks": 12,
      "completed": 5,
      "failed": 0,
      "skipped": 0,
      "output_dir": "/downloads",
      "meta_name": "Album Name"
    }
  ],
  "count": 1
}
```

---

### Get Job Detail

```
GET /api/v1/jobs/{job_id}
```

**Response:**

```json
{
  "id": "job-1712476800000000000",
  "status": "completed",
  "spotify_url": "https://open.spotify.com/album/...",
  "spotify_type": "album",
  "service": "tidal",
  "audio_format": "LOSSLESS",
  "created_at": "2026-04-07T12:00:00Z",
  "updated_at": "2026-04-07T12:05:00Z",
  "completed_at": "2026-04-07T12:05:00Z",
  "total_tracks": 3,
  "completed": 3,
  "failed": 0,
  "skipped": 0,
  "output_dir": "/downloads",
  "meta_name": "Album Name",
  "tracks": [
    {
      "track_name": "Track One",
      "artist_name": "Artist",
      "spotify_id": "abc123",
      "status": "completed",
      "file_path": "/downloads/Track One - Artist.flac",
      "file_size": 35420160,
      "drive_file_id": "1a2b3c4d5e",
      "drive_link": "https://drive.google.com/file/d/1a2b3c4d5e/view"
    },
    {
      "track_name": "Track Two",
      "artist_name": "Artist",
      "spotify_id": "def456",
      "status": "completed",
      "file_path": "/downloads/Track Two - Artist.flac",
      "file_size": 28311552
    },
    {
      "track_name": "Track Three",
      "artist_name": "Artist",
      "spotify_id": "ghi789",
      "status": "failed",
      "error": "songlink couldn't find Tidal URL"
    }
  ]
}
```

**Job Status Values:**

| Status | Description |
|--------|-------------|
| `pending` | Job created, not yet started |
| `fetching_metadata` | Fetching track list from Spotify |
| `downloading` | Downloading tracks |
| `completed` | All tracks processed |
| `failed` | Job-level failure (e.g. metadata fetch failed) |
| `cancelled` | Job was cancelled |

**Track Status Values:**

| Status | Description |
|--------|-------------|
| `pending` | Waiting to download |
| `downloading` | Currently downloading |
| `completed` | Downloaded successfully |
| `failed` | Download failed (check `error` field) |
| `cancelled` | Cancelled |

---

### Cancel Job

```
POST /api/v1/jobs/{job_id}/cancel
```

**Response:**

```json
{
  "message": "job cancelled"
}
```

---

### Delete Job

Remove a job from the list.

```
DELETE /api/v1/jobs/{job_id}
```

**Response:**

```json
{
  "message": "job deleted"
}
```

---

### Check Track Availability

Check which services have a track available.

```
GET /api/v1/availability?spotify_id={spotify_id}
```

**Example:**

```bash
curl http://localhost:8080/api/v1/availability?spotify_id=4iV5W9uYEdYUVa79Axb7Rh
```

**Response:**

```json
{
  "spotify_id": "4iV5W9uYEdYUVa79Axb7Rh",
  "tidal": true,
  "amazon": true,
  "qobuz": false,
  "deezer": true,
  "tidal_url": "https://tidal.com/track/...",
  "amazon_url": "https://music.amazon.com/...",
  "qobuz_url": "",
  "deezer_url": "https://deezer.com/track/..."
}
```

---

### Cleanup Old Jobs

Remove completed jobs older than 24 hours.

```
POST /api/v1/cleanup
```

**Response:**

```json
{
  "removed": 5,
  "message": "cleaned up completed jobs older than 24 hours"
}
```

## Google Drive Setup

### 1. Create a Service Account

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project (or select existing)
3. Enable the **Google Drive API**
4. Go to **IAM & Admin > Service Accounts**
5. Click **Create Service Account**
6. Give it a name (e.g. `spotiflac-uploader`)
7. Click **Done** (no extra roles needed)
8. Click on the service account > **Keys** > **Add Key** > **Create new key** > **JSON**
9. Save the downloaded JSON file as `service-account.json`

### 2. Share Your Drive Folder

1. Create a folder in Google Drive where uploads will go
2. Right-click the folder > **Share**
3. Add the service account email (e.g. `spotiflac-uploader@project.iam.gserviceaccount.com`) as **Editor**
4. Copy the folder ID from the URL: `https://drive.google.com/drive/folders/{THIS_IS_THE_FOLDER_ID}`

### 3. Configure

```bash
GDRIVE_ENABLED=true
GDRIVE_CREDENTIALS_FILE=./service-account.json
GDRIVE_ROOT_FOLDER_ID=your-folder-id-here
GDRIVE_DELETE_AFTER_UPLOAD=true  # removes local files after upload
```

### Upload Behavior

- Each album/playlist/artist creates a subfolder in the root Drive folder
- Single track downloads go into a date-based folder (e.g. `SpotiFLAC-2026-04-07`)
- You can override the folder per-request with `drive_folder_id`
- Upload happens immediately after each track finishes downloading
- If `delete_after_upload` is `true`, local files are removed after successful upload
- Drive file ID and link are available in the job detail response per track
