package downloader

// DownloadProgress is the byte-level progress snapshot exposed by
// HTTPDownloader. It mirrors the Herdsman downloader contract so HitK can use
// the same polling and progress semantics.
type DownloadProgress struct {
	ID            string
	URL           string
	DestPath      string
	Progress      float64
	Speed         float64
	Size          int64
	Downloaded    int64
	Status        string
	Error         string
	StartTime     int64
	CurrentTime   int64
	EstimatedTime int64
}
