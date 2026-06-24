package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/Laky-64/gologging"
	"github.com/amarnathcjd/gogram/telegram"

	"main/internal/cookies"
	state "main/internal/core/models"
)

const PlatformYtDlp state.PlatformName = "YtDlp"

type YtdlpPlatform struct {
	name state.PlatformName
}

type ytdlpInfo struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Duration    float64     `json:"duration"`
	Thumbnail   string      `json:"thumbnail"`
	URL         string      `json:"webpage_url"`
	OriginalURL string      `json:"original_url"`
	Uploader    string      `json:"uploader"`
	Description string      `json:"description"`
	IsLive      bool        `json:"is_live"`
	Entries     []ytdlpInfo `json:"entries"`
}

type CobaltRequest struct {
	URL          string `json:"url"`
	VideoQuality string `json:"videoQuality"`
	DownloadMode string `json:"downloadMode"`
}

type CobaltResponse struct {
	Status string `json:"status"`
	URL    string `json:"url"`
	Text   string `json:"text"`
}

// URLs that are likely handled by YouTube
var youtubePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(youtube\.com|youtu\.be|music\.youtube\.com)`),
}

// Regex to accurately pull Video IDs from various YouTube URL formats (shorts, embed, live, etc.)
var ytRegex = regexp.MustCompile(`(?:youtube\.com\/(?:[^\/]+\/.+\/|(?:v|e(?:mbed)?|shorts|live)\/|.*[?&]v=)|youtu\.be\/)([^"&?\/\s]{11})`)

func init() {
	Register(60, &YtdlpPlatform{
		name: PlatformYtDlp,
	})
}

func (y *YtdlpPlatform) Name() state.PlatformName {
	return y.name
}

// CanGetTracks checks if this is a valid URL that yt-dlp might handle
func (y *YtdlpPlatform) CanGetTracks(query string) bool {
	query = strings.TrimSpace(query)

	// Must be a URL
	parsedURL, err := url.Parse(query)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return false
	}

	host := strings.ToLower(parsedURL.Host)

	// Ignore Telegram URLs ( already handled by TeleramPlatform)
	if host == "t.me" ||
		host == "telegram.me" ||
		host == "telegram.dog" ||
		strings.HasSuffix(host, ".t.me") {
		return false
	}

	return true
}

// GetTracks extracts metadata using yt-dlp
func (y *YtdlpPlatform) GetTracks(
	query string,
	video bool,
) ([]*state.Track, error) {
	query = strings.TrimSpace(query)

	gologging.InfoF("YtDlp: Extracting metadata for %s", query)

	info, err := y.extractMetadata(query)
	if err != nil {
		gologging.ErrorF("YtDlp: Failed to extract metadata: %v", err)
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Check if it's a live stream
	if info.IsLive {
		gologging.Info("YtDlp: Detected live stream, returning error")
		return nil, errors.New(
			"live streams are not supported by yt-dlp downloader",
		)
	}

	var tracks []*state.Track

	// Handle playlists
	if len(info.Entries) > 0 {
		gologging.InfoF(
			"YtDlp: Found playlist with %d entries",
			len(info.Entries),
		)
		for _, entry := range info.Entries {
			if entry.IsLive {
				continue // Skip live entries
			}
			track := y.infoToTrack(&entry, video)
			tracks = append(tracks, track)
		}
	} else {
		track := y.infoToTrack(info, video)
		tracks = []*state.Track{track}
	}

	if len(tracks) > 0 {
		gologging.InfoF(
			"YtDlp: Successfully extracted %d track(s)",
			len(tracks),
		)
	}

	return tracks, nil
}

func (y *YtdlpPlatform) CanDownload(source state.PlatformName) bool {
	// YtDlp can download from itself (when it extracts info)
	// and from YouTube platform
	return source == y.name || source == PlatformYouTube
}

func (y *YtdlpPlatform) Download(
	ctx context.Context,
	track *state.Track,
	_ *telegram.NewMessage,
) (string, error) {
	if f := findFile(track); f != "" {
		gologging.Debug("Ytdlp: Download -> Cached File -> " + f)
		return f, nil
	}

	gologging.InfoF("YtDlp: Downloading %s", track.Title)

	// Build definitive static extension file path names
	outputPath := getPath(track, ".mp3")
	if track.Video {
		outputPath = getPath(track, ".mp4")
	}

	// COBALT API BYPASS: Executes for audio files to avoid local binary drops
	if !track.Video {
		gologging.InfoF("YtDlp: Bypassing local binary via Cobalt API node for: %s", track.URL)
		err := y.downloadViaCobalt(ctx, track.URL, outputPath)
		if err == nil {
			gologging.InfoF("YtDlp: Cobalt extraction completed successfully -> %s", outputPath)
			return outputPath, nil
		}
		gologging.ErrorF("YtDlp: Cobalt node extraction failed: %v. Running native binary fallback...", err)
	}

	args := []string{
		"--no-playlist",
		"--no-part",
		"--geo-bypass",
		"--no-warnings",
		"--ignore-errors",
		"--no-check-certificate",
		"-q",
		"-o", getPath(track, ".%(ext)s"),
	}

	// Format selection
	if track.Video {
		args = append(
			args,
			"-f",
			"(b[height>=360][height<=1080]/bv*[height>=360][height<=1080]/bv*)+(ba[abr>=180][abr<=360]/ba)/b",
		)
	} else {
		args = append(args,
			"-f", "ba[abr>=180][abr<=360]/ba",
			"-x",
			"--concurrent-fragments", "4",
		)
	}

	// Cookies (YouTube only)
	if y.isYouTubeURL(track.URL) {
		if cookieFile, err := cookies.GetRandomCookieFile(); err == nil &&
			cookieFile != "" {
			args = append(args, "--cookies", cookieFile)
		}
	}

	args = append(args, track.URL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := strings.TrimSpace(stderr.String())
		outStr := strings.TrimSpace(stdout.String())

		gologging.ErrorF(
			"YtDlp: Download failed for %s: %v\nSTDOUT:\n%s\nSTDERR:\n%s",
			track.URL, err, outStr, errStr,
		)
		findAndRemove(track)

		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}

		return "", fmt.Errorf("yt-dlp error: %w", err)
	}

	path := findFile(track)
	if path == "" {
		return "", errors.New("yt-dlp did not return output file path")
	}

	gologging.InfoF("YtDlp: Successfully downloaded %s", path)
	return path, nil
}

func (*YtdlpPlatform) CanSearch() bool { return false }

func (*YtdlpPlatform) Search(
	string,
	bool,
) ([]*state.Track, error) {
	return nil, nil
}

// extractMetadata uses a public backend API to pull video information, completely bypassing local binary blocks
func (y *YtdlpPlatform) extractMetadata(urlStr string) (*ytdlpInfo, error) {
	// Identify the unique YouTube Video ID out of the raw query URL string safely via regex
	videoID := ""
	matches := ytRegex.FindStringSubmatch(urlStr)
	if len(matches) > 1 {
		videoID = matches[1]
	}

	// Fallback implementation: If parsing unique ID fails, attempt a direct fallback sequence
	if videoID == "" {
		gologging.Warn("YtDlp: Could not cleanly parse Video ID. Using standard yt-dlp binary fallback wrapper.")
		return y.legacyExtractMetadata(urlStr)
	}

	// Query standard public configuration targets to retrieve title, thumbnail, and playback layouts safely
	apiURL := fmt.Sprintf("https://noembed.com/embed?url=https://www.youtube.com/watch?v=%s", videoID)

	resp, err := http.Get(apiURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		gologging.WarnF("YtDlp: API failed or returned non-200, falling back to binary. Error: %v", err)
		if resp != nil {
			resp.Body.Close()
		}
		return y.legacyExtractMetadata(urlStr)
	}
	defer resp.Body.Close()

	var result struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		gologging.WarnF("YtDlp: failed to parse web stream metadata, falling back to binary. Error: %v", err)
		return y.legacyExtractMetadata(urlStr)
	}

	// Populate the engine model structs expected by your music playback core system
	var info ytdlpInfo
	info.ID = videoID
	info.Title = result.Title
	info.Uploader = result.AuthorName
	info.Thumbnail = result.ThumbnailURL
	info.URL = fmt.Sprintf("https://youtube.com/watch?v=%s", videoID)
	info.OriginalURL = info.URL
	info.Duration = 240 // Placeholder value (4 mins) to prevent empty initialization crashes
	info.IsLive = false

	return &info, nil
}

// legacyExtractMetadata contains your original binary extraction array as a safe local network wrapper fallback
func (y *YtdlpPlatform) legacyExtractMetadata(urlStr string) (*ytdlpInfo, error) {
	args := []string{
		"-j",
		"--flat-playlist",
		"--no-warnings",
		"--no-check-certificate",
	}

	// Add cookies only for YouTube
	if y.isYouTubeURL(urlStr) {
		cookieFile, err := cookies.GetRandomCookieFile()
		if err == nil && cookieFile != "" {
			args = append(args, "--cookies", cookieFile)
		}
	}

	args = append(args, urlStr)

	cmd := exec.Command("yt-dlp", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := stderr.String()
		gologging.ErrorF(
			"YtDlp: Metadata extraction failed: %v\n%s",
			err,
			errStr,
		)
		return nil, fmt.Errorf("metadata extraction failed: %w", err)
	}

	output := stdout.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Handle playlists (multiple JSON objects)
	if len(lines) > 1 {
		var info ytdlpInfo
		info.Entries = make([]ytdlpInfo, 0, len(lines))

		for _, line := range lines {
			var entry ytdlpInfo
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				gologging.ErrorF("YtDlp: Failed to parse entry JSON: %v", err)
				continue
			}
			info.Entries = append(info.Entries, entry)
		}

		if len(info.Entries) == 0 {
			return nil, errors.New("no valid entries found in playlist")
		}

		return &info, nil
	}

	// Single video/audio
	var info ytdlpInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		gologging.ErrorF("YtDlp: Failed to parse JSON: %v", err)
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &info, nil
}

// infoToTrack converts yt-dlp info to Track
func (y *YtdlpPlatform) infoToTrack(
	info *ytdlpInfo,
	video bool,
) *state.Track {
	duration := int(info.Duration)

	// Use original_url if available, otherwise webpage_url
	trackURL := info.URL
	if info.OriginalURL != "" {
		trackURL = info.OriginalURL
	}

	return &state.Track{
		ID:       info.ID,
		Title:    info.Title,
		Duration: duration,
		Artwork:  info.Thumbnail,
		URL:      trackURL,
		Source:   PlatformYtDlp,
		Video:    video,
	}
}

// isYouTubeURL checks if the URL is from YouTube
func (y *YtdlpPlatform) isYouTubeURL(urlStr string) bool {
	for _, pattern := range youtubePatterns {
		if pattern.MatchString(urlStr) {
			return true
		}
	}
	return false
}

// downloadViaCobalt contacts alternate Cobalt infrastructure to bypass YouTube blocks
func (y *YtdlpPlatform) downloadViaCobalt(ctx context.Context, targetURL string, destPath string) error {
	// Switching to a more stable mirror node to prevent cloud processing blocks
	apiEndpoint := "https://wuk.sh"

	reqPayload := CobaltRequest{
		URL:          targetURL,
		VideoQuality: "720",
		DownloadMode: "audio",
	}

	jsonData, err := json.Marshal(reqPayload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	
	// Adding explicit browser headers so the proxy node accepts our request
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var cobResp CobaltResponse
	if err := json.NewDecoder(resp.Body).Decode(&cobResp); err != nil {
		return fmt.Errorf("mirror response failed to decode: %w", err)
	}

	if cobResp.Status == "error" || cobResp.URL == "" {
		return fmt.Errorf("cobalt API mirror internal error: %s", cobResp.Text)
	}

	fileReq, err := http.NewRequestWithContext(ctx, "GET", cobResp.URL, nil)
	if err != nil {
		return err
	}
	fileReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	fileResp, err := client.Do(fileReq)
	if err != nil {
		return err
	}
	defer fileResp.Body.Close()

	if fileResp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad audio server response stream code: %d", fileResp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, fileResp.Body)
	return err
}
