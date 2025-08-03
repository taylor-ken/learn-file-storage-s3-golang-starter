package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form", err)
		return
	}
	file, handler, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't get video", err)
		return
	}
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not video owner", err)
		return
	}

	contentType := handler.Header.Get("Content-Type")
	extensions, err := mime.ExtensionsByType(contentType)
	if err != nil || len(extensions) == 0 {
		respondWithError(w, http.StatusBadRequest, "Invalid file extension", err)
		return
	}
	filename := videoID.String() + extensions[0]
	new_file, err := os.Create(filepath.Join(cfg.assetsRoot, filename))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing file", err)
		return
	}
	defer new_file.Close()
	io.Copy(new_file, file)

	url := "/assets/" + filename
	videoMetadata.ThumbnailURL = &url
	// videoMetadata.ThumbnailURL = fmt.Sprintf("http://localhost:%d/assets/%s", cfg.port, filename)
	err = cfg.db.UpdateVideo(videoMetadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetadata)
}
