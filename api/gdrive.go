package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type DriveConfig struct {
	Enabled            bool   `json:"enabled"`
	CredentialsFile    string `json:"-"`
	RootFolderID       string `json:"root_folder_id"`
	DeleteAfterUpload  bool   `json:"delete_after_upload"`
}

type DriveClient struct {
	service *drive.Service
	config  DriveConfig
}

func NewDriveClient(cfg DriveConfig) (*DriveClient, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	credFile := cfg.CredentialsFile
	if credFile == "" {
		credFile = os.Getenv("GDRIVE_CREDENTIALS_FILE")
	}
	if credFile == "" {
		credFile = "/config/service-account.json"
	}

	if _, err := os.Stat(credFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("service account credentials file not found: %s", credFile)
	}

	ctx := context.Background()
	service, err := drive.NewService(ctx, option.WithCredentialsFile(credFile), option.WithScopes(drive.DriveFileScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}

	return &DriveClient{
		service: service,
		config:  cfg,
	}, nil
}

// Upload uploads a local file to Google Drive and returns the file ID and web link.
func (dc *DriveClient) Upload(ctx context.Context, localPath string, parentFolderID string) (fileID string, webLink string, err error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	fileName := filepath.Base(localPath)

	parent := parentFolderID
	if parent == "" {
		parent = dc.config.RootFolderID
	}

	meta := &drive.File{
		Name: fileName,
	}
	if parent != "" {
		meta.Parents = []string{parent}
	}

	created, err := dc.service.Files.Create(meta).
		Context(ctx).
		Media(f).
		Fields("id, webViewLink, webContentLink").
		Do()
	if err != nil {
		return "", "", fmt.Errorf("drive upload failed: %w", err)
	}

	link := created.WebViewLink
	if link == "" {
		link = created.WebContentLink
	}

	return created.Id, link, nil
}

// FindOrCreateFolder finds a folder by name under parentID, or creates it.
func (dc *DriveClient) FindOrCreateFolder(ctx context.Context, name string, parentID string) (string, error) {
	parent := parentID
	if parent == "" {
		parent = dc.config.RootFolderID
	}

	// Search for existing folder
	query := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and trashed=false", name)
	if parent != "" {
		query += fmt.Sprintf(" and '%s' in parents", parent)
	}

	result, err := dc.service.Files.List().
		Context(ctx).
		Q(query).
		Fields("files(id)").
		PageSize(1).
		Do()
	if err != nil {
		return "", fmt.Errorf("drive folder search failed: %w", err)
	}

	if len(result.Files) > 0 {
		return result.Files[0].Id, nil
	}

	// Create folder
	meta := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}
	if parent != "" {
		meta.Parents = []string{parent}
	}

	folder, err := dc.service.Files.Create(meta).
		Context(ctx).
		Fields("id").
		Do()
	if err != nil {
		return "", fmt.Errorf("drive folder creation failed: %w", err)
	}

	return folder.Id, nil
}

// UploadWithJobFolder uploads a file into a job-specific subfolder.
// Returns fileID, webLink, error.
func (dc *DriveClient) UploadWithJobFolder(ctx context.Context, localPath string, jobName string) (string, string, error) {
	folderName := jobName
	if folderName == "" {
		folderName = "SpotiFLAC-" + time.Now().Format("2006-01-02")
	}

	folderID, err := dc.FindOrCreateFolder(ctx, folderName, "")
	if err != nil {
		return "", "", err
	}

	fileID, link, err := dc.Upload(ctx, localPath, folderID)
	if err != nil {
		return "", "", err
	}

	// Clean up local file if configured
	if dc.config.DeleteAfterUpload {
		if removeErr := os.Remove(localPath); removeErr != nil {
			log.Printf("Warning: failed to delete local file after upload: %v", removeErr)
		}
	}

	return fileID, link, nil
}
