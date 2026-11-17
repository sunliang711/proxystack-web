package domain

// ClashProvider holds the native HTTP provider fields emitted for a stack.
type ClashProvider struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Path          string `json:"path"`
	Proxy         string `json:"proxy"`
	Filter        string `json:"filter"`
	ExcludeFilter string `json:"exclude_filter"`
	DownloadStack string `json:"download_stack,omitempty"`
}
