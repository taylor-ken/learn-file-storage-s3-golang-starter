package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}

	file, handler, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(handler.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type, only MP4 is allowed", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not write file to disk", err)
		return
	}
	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get aspect ratio", err)
		return
	}
	prefix := "other/"
	if ratio == "16:9" {
		prefix = "landscape/"
	}
	if ratio == "9:16" {
		prefix = "portrait/"
	}
	output_filepath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		fmt.Printf("Error processing video for fast start: %v\n", err)
		return
	}

	out_file, err := os.Open(output_filepath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer os.Remove(out_file.Name())
	defer out_file.Close()
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset file pointer", err)
		return
	}

	key := fmt.Sprintf("%s%s/%s.mp4", prefix, userID.String(), videoID.String())
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        out_file,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		log.Printf("S3 PutObject error: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}
	url := "http://" + cfg.s3CfDistribution + "/" + key
	video.VideoURL = &url

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
	}
}

func getVideoAspectRatio(filePath string) (string, error) {
	var out bytes.Buffer

	type Stream struct {
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		CodecType string `json:"codec_type"`
	}

	type FFProbeOutput struct {
		Streams []Stream `json:"streams"`
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("couldn't execute command")
	}
	var p FFProbeOutput
	err = json.Unmarshal(out.Bytes(), &p)
	if err != nil {
		return "", fmt.Errorf("couldn't unmarshall")
	}
	if len(p.Streams) < 1 {
		return "", fmt.Errorf("no streams found in video")
	}
	ratio := float64(p.Streams[0].Width) / float64(p.Streams[0].Height)

	if ratio > 1.75 && ratio < 1.80 {
		return "16:9", nil
	}
	if ratio > 0.53 && ratio < 0.58 {
		return "9:16", nil
	}
	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	output_filepath := filePath + ".processing"
	cmd_out := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", output_filepath)

	var stderr bytes.Buffer
	cmd_out.Stderr = &stderr

	err := cmd_out.Run()
	if err != nil {
		return "", fmt.Errorf("couldn't execute command")
	}
	return output_filepath, nil
}
