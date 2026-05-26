package notifier

import (
	"fmt"
	"strings"

	"github.com/mescon/Healarr/internal/domain"
)

// messageContext holds extracted data for message formatting
type messageContext struct {
	FilePath       string
	FileName       string
	CorruptionType string
	ScanPath       string
	Healthy        int
	Corrupt        int
	Total          int
	RetryCount     int
	MaxRetries     int
	ErrorMsg       string
	Reason         string
	Attempts       int
}

// extractMessageContext extracts common fields from event data
func extractMessageContext(data map[string]interface{}) messageContext {
	filePath, _ := data["file_path"].(string)
	fileName := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		fileName = filePath[idx+1:]
	}

	ctx := messageContext{
		FilePath: filePath,
		FileName: fileName,
	}
	ctx.CorruptionType, _ = data["corruption_type"].(string)
	ctx.ScanPath, _ = data["path"].(string)
	ctx.Healthy = extractInt(data, "healthy_files")
	ctx.Corrupt = extractInt(data, "corrupt_files")
	ctx.Total = extractInt(data, "total_files")
	ctx.RetryCount = extractInt(data, "retry_count")
	ctx.MaxRetries = extractInt(data, "max_retries")
	ctx.Attempts = extractInt(data, "attempts")
	ctx.ErrorMsg, _ = data["error"].(string)
	ctx.Reason, _ = data["reason"].(string)

	return ctx
}

// extractInt extracts an int from a map, handling both int and float64 (from JSON).
func extractInt(data map[string]interface{}, key string) int {
	if v, ok := data[key].(int); ok {
		return v
	}
	if v, ok := data[key].(float64); ok {
		return int(v)
	}
	return 0
}

// messageFormatter is a function type for formatting event messages
type messageFormatter func(ctx messageContext) string

// messageFormatters maps event types to their message formatters
var messageFormatters = map[string]messageFormatter{
	string(domain.ScanStarted):          fmtScanStarted,
	string(domain.ScanCompleted):        fmtScanCompleted,
	string(domain.ScanFailed):           fmtScanFailed,
	string(domain.CorruptionDetected):   fmtCorruptionDetected,
	string(domain.RemediationQueued):    fmtRemediationQueued,
	string(domain.DeletionStarted):      fmtDeletionStarted,
	string(domain.DeletionCompleted):    fmtDeletionCompleted,
	string(domain.DeletionFailed):       fmtDeletionFailed,
	string(domain.SearchStarted):        fmtSearchStarted,
	string(domain.SearchCompleted):      fmtSearchCompleted,
	string(domain.SearchFailed):         fmtSearchFailed,
	string(domain.VerificationStarted):  fmtVerificationStarted,
	string(domain.VerificationSuccess):  fmtVerificationSuccess,
	string(domain.VerificationFailed):   fmtVerificationFailed,
	string(domain.DownloadTimeout):      fmtDownloadTimeout,
	string(domain.ImportBlocked):        fmtImportBlocked,
	string(domain.ManuallyRemoved):      fmtManuallyRemoved,
	string(domain.DownloadIgnored):      fmtDownloadIgnored,
	string(domain.RetryScheduled):       fmtRetryScheduled,
	string(domain.MaxRetriesReached):    fmtMaxRetriesReached,
	string(domain.SearchExhausted):      fmtSearchExhausted,
	string(domain.DownloadFailed):       fmtDownloadFailed,
	string(domain.SystemHealthDegraded): fmtSystemHealthDegraded,
	string(domain.InstanceUnhealthy):    fmtInstanceUnhealthy,
	string(domain.InstanceHealthy):      fmtInstanceHealthy,
	string(domain.StuckRemediation):     fmtStuckRemediation,
	string(domain.CorruptionIgnored):    fmtCorruptionIgnored,
}

func fmtScanStarted(ctx messageContext) string {
	return fmt.Sprintf("🔍 Scan started: %s", ctx.ScanPath)
}

func fmtScanCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ Scan complete: %s\n📊 %d/%d healthy, %d corrupt", ctx.ScanPath, ctx.Healthy, ctx.Total, ctx.Corrupt)
}

func fmtScanFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Scan failed: %s\n⚠️ %s", ctx.ScanPath, ctx.ErrorMsg)
}

func fmtCorruptionDetected(ctx messageContext) string {
	msg := fmt.Sprintf("🔴 Corruption detected: %s", ctx.FileName)
	if ctx.CorruptionType != "" {
		msg += fmt.Sprintf("\n📋 Type: %s", ctx.CorruptionType)
	}
	return msg
}

func fmtRemediationQueued(ctx messageContext) string {
	return fmt.Sprintf("🔧 Remediation queued: %s", ctx.FileName)
}

func fmtDeletionStarted(ctx messageContext) string {
	return fmt.Sprintf("🗑️ Deletion started: %s", ctx.FileName)
}

func fmtDeletionCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ File deleted for re-download: %s", ctx.FileName)
}

func fmtDeletionFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Deletion failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtSearchStarted(ctx messageContext) string {
	return fmt.Sprintf("🔎 Search triggered in *arr: %s", ctx.FileName)
}

func fmtSearchCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ Search completed: %s", ctx.FileName)
}

func fmtSearchFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Search failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtVerificationStarted(ctx messageContext) string {
	return fmt.Sprintf("🔬 Verification started: %s", ctx.FileName)
}

func fmtVerificationSuccess(ctx messageContext) string {
	return fmt.Sprintf("✅ File verified healthy: %s", ctx.FileName)
}

func fmtVerificationFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Verification failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtDownloadTimeout(ctx messageContext) string {
	return fmt.Sprintf("⏰ Download timeout: %s", ctx.FileName)
}

func fmtImportBlocked(ctx messageContext) string {
	return fmt.Sprintf("🚫 Import blocked in *arr: %s\n⚠️ %s\n👉 Manual intervention required in Sonarr/Radarr", ctx.FileName, ctx.ErrorMsg)
}

func fmtManuallyRemoved(ctx messageContext) string {
	return fmt.Sprintf("🗑️ Download manually removed: %s\n👉 Item was removed from *arr queue without being imported", ctx.FileName)
}

func fmtDownloadIgnored(ctx messageContext) string {
	return fmt.Sprintf("⏸️ Download ignored by user: %s\n👉 User marked download as ignored in *arr - remediation stopped", ctx.FileName)
}

func fmtRetryScheduled(ctx messageContext) string {
	return fmt.Sprintf("🔄 Retry scheduled (%d/%d): %s", ctx.RetryCount, ctx.MaxRetries, ctx.FileName)
}

func fmtMaxRetriesReached(ctx messageContext) string {
	return fmt.Sprintf("⚠️ Max retries exhausted (%d): %s", ctx.MaxRetries, ctx.FileName)
}

func fmtSearchExhausted(ctx messageContext) string {
	msg := fmt.Sprintf("🔍 No replacement found: %s", ctx.FileName)
	if ctx.Attempts > 0 {
		msg += fmt.Sprintf("\n📊 Attempts: %d", ctx.Attempts)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	msg += "\n👉 Check your indexers or manually search in Sonarr/Radarr"
	return msg
}

func fmtDownloadFailed(ctx messageContext) string {
	msg := fmt.Sprintf("❌ Download failed: %s", ctx.FileName)
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf("\n⚠️ %s", ctx.ErrorMsg)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	return msg
}

func fmtSystemHealthDegraded(ctx messageContext) string {
	msg := "⚠️ System health degraded"
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.ErrorMsg)
	}
	return msg
}

func fmtInstanceUnhealthy(ctx messageContext) string {
	msg := "🔴 Arr instance unreachable"
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf("\n⚠️ %s", ctx.ErrorMsg)
	}
	return msg
}

func fmtInstanceHealthy(ctx messageContext) string {
	msg := "🟢 Arr instance recovered"
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	return msg
}

func fmtStuckRemediation(ctx messageContext) string {
	msg := "⏰ Stuck remediation detected"
	if ctx.FilePath != "" {
		msg += fmt.Sprintf(": %s", ctx.FileName)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	return msg + "\n👉 Remediation has shown no progress - manual check recommended"
}

func fmtCorruptionIgnored(ctx messageContext) string {
	msg := fmt.Sprintf("🙈 Corruption ignored: %s", ctx.FileName)
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	return msg
}

func (n *Notifier) formatMessage(eventType string, data map[string]interface{}) string {
	ctx := extractMessageContext(data)
	if formatter, ok := messageFormatters[eventType]; ok {
		return formatter(ctx)
	}
	return fmt.Sprintf("📢 Event: %s", eventType)
}

// GenericWebhookPayload is the rich JSON payload sent to generic webhooks
