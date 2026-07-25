package valueobject

type ScanVerdict string

const (
	ScanVerdictClean       ScanVerdict = "CLEAN"
	ScanVerdictSpamJunk    ScanVerdict = "SPAM_JUNK"
	ScanVerdictSpamReject  ScanVerdict = "SPAM_REJECT"
	ScanVerdictGreylist    ScanVerdict = "GREYLIST"
	ScanVerdictVirusReject ScanVerdict = "VIRUS_REJECT"
)

type ScanResult struct {
	Verdict       ScanVerdict
	Score         float64
	RequiredScore float64
	Action        string
	VirusName     string
	Symbols       map[string]float64
	HeadersToAdd  map[string]string
}

func NewCleanScanResult() *ScanResult {
	return &ScanResult{
		Verdict:      ScanVerdictClean,
		Symbols:      make(map[string]float64),
		HeadersToAdd: make(map[string]string),
	}
}

func NewVirusScanResult(virusName string) *ScanResult {
	return &ScanResult{
		Verdict:      ScanVerdictVirusReject,
		VirusName:    virusName,
		Symbols:      make(map[string]float64),
		HeadersToAdd: make(map[string]string),
	}
}
