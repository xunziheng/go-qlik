package report

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"github.com/soderasen-au/go-common/util"
)

// libreOfficeProfileURL encodes an absolute filesystem path as a file URL.
// Keep the filesystem path unchanged; only the LibreOffice argument is escaped.
func libreOfficeProfileURL(absPath string) string {
	path := filepath.ToSlash(absPath)
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// ExcelToPDFTaskConfig holds configuration for a single Excel-to-PDF conversion task
type ExcelToPDFTaskConfig struct {
	// Input/Output
	InputExcelPath string `json:"input_excel_path" yaml:"input_excel_path" bson:"input_excel_path"` // Path to input Excel file
	OutputPDFPath  string `json:"output_pdf_path" yaml:"output_pdf_path" bson:"output_pdf_path"`    // Path to output PDF file

	// Sheet selection (subset of Windows options - LibreOffice converts all sheets)
	// Note: LibreOffice headless mode converts entire workbook. Sheet-specific export not supported.

	// Export options (limited compared to Windows COM)
	// Note: Page setup, margins, orientation are embedded in Excel file - LibreOffice respects them

	// Logger
	Logger *zerolog.Logger `json:"-" yaml:"-" bson:"-"`
}

// Validate checks configuration validity
func (c *ExcelToPDFTaskConfig) Validate() *util.Result {
	if c.InputExcelPath == "" {
		return util.MsgError("Validate", "InputExcelPath is required")
	}
	if c.OutputPDFPath == "" {
		return util.MsgError("Validate", "OutputPDFPath is required")
	}

	// Verify input file exists
	if _, err := os.Stat(c.InputExcelPath); os.IsNotExist(err) {
		return util.Error("Validate", fmt.Errorf("input file does not exist: %s", c.InputExcelPath))
	}

	return nil
}

// LibreExcel2PDF handles cross-platform Excel-to-PDF conversion using LibreOffice
type LibreExcel2PDF struct {
	libreOfficeBin string
	logger         *zerolog.Logger
	maxConcurrent  int
	tempFolder     string // Base temp folder for worker profile directories

	// Concurrency control
	mu        sync.Mutex
	started   bool
	taskQueue chan *conversionTask
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

// conversionTask represents a single conversion request
type conversionTask struct {
	ctx    context.Context
	config ExcelToPDFTaskConfig
	result chan *util.Result
}

var (
	globalInstance     *LibreExcel2PDF
	globalInstanceOnce sync.Once
	globalInstanceMu   sync.RWMutex
)

// NewLibreExcel2PDF returns a singleton global instance of LibreOffice-based Excel-to-PDF converter
// The first call initializes the singleton with the provided parameters
// Subsequent calls return the same instance (ignoring new parameters)
func NewLibreExcel2PDF(libreOfficeBin string, logger *zerolog.Logger, maxConcurrent int, tempFolder string) *LibreExcel2PDF {
	globalInstanceOnce.Do(func() {
		if libreOfficeBin == "" {
			libreOfficeBin = "libreoffice"
		}

		if logger == nil {
			nopLogger := zerolog.Nop()
			logger = &nopLogger
		}

		if maxConcurrent <= 0 {
			maxConcurrent = 1
		}

		if tempFolder == "" {
			tempFolder = os.TempDir()
		}

		globalInstance = &LibreExcel2PDF{
			libreOfficeBin: libreOfficeBin,
			logger:         logger,
			maxConcurrent:  maxConcurrent,
			tempFolder:     tempFolder,
			started:        false,
		}
	})

	return globalInstance
}

// ResetGlobalInstance resets the singleton instance (useful for testing)
// WARNING: Only call this when you're certain no conversions are in progress
func ResetGlobalInstance() {
	globalInstanceMu.Lock()
	defer globalInstanceMu.Unlock()

	if globalInstance != nil && globalInstance.started {
		globalInstance.Shutdown(context.Background())
	}

	globalInstance = nil
	globalInstanceOnce = sync.Once{}
}

// StartUp initializes and starts the main loop for handling conversion requests
func (l *LibreExcel2PDF) StartUp(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.started {
		l.logger.Warn().Msg("LibreExcel2PDF already started")
		return
	}

	l.ctx, l.cancel = context.WithCancel(ctx)
	l.taskQueue = make(chan *conversionTask, l.maxConcurrent*2)
	l.started = true

	// Start worker pool
	for i := 0; i < l.maxConcurrent; i++ {
		l.wg.Add(1)
		go l.worker(i)
	}

	l.logger.Info().Int("workers", l.maxConcurrent).Msg("LibreExcel2PDF started")
}

// worker processes conversion tasks from the queue
func (l *LibreExcel2PDF) worker(id int) {
	defer l.wg.Done()

	workerLogger := l.logger.With().Int("worker", id).Logger()
	workerLogger.Debug().Msg("worker started")

	// Create worker-specific user profile directory
	workerProfileDir := filepath.Join(l.tempFolder, fmt.Sprintf("libreoffice-worker-%d", id))
	if err := os.MkdirAll(workerProfileDir, 0755); err != nil {
		workerLogger.Err(err).Str("profile_dir", workerProfileDir).Msg("failed to create worker profile directory")
		// Continue anyway - executeConversion will handle the error
	} else {
		workerLogger.Debug().Str("profile_dir", workerProfileDir).Msg("worker profile directory created")
	}

	for {
		select {
		case <-l.ctx.Done():
			workerLogger.Debug().Msg("worker shutting down")
			return

		case task, ok := <-l.taskQueue:
			if !ok {
				workerLogger.Debug().Msg("task queue closed, worker exiting")
				return
			}

			// Process the task with worker-specific profile directory
			result := l.executeConversion(task.ctx, task.config, workerProfileDir, workerLogger)

			// Send result back
			select {
			case task.result <- result:
			case <-task.ctx.Done():
				// Caller context cancelled, don't block
			}
		}
	}
}

// executeConversion performs the actual LibreOffice conversion
func (l *LibreExcel2PDF) executeConversion(ctx context.Context, config ExcelToPDFTaskConfig, workerProfileDir string, workerLogger zerolog.Logger) *util.Result {
	logger := workerLogger
	if ctxLogger := ctx.Value("ctxLogger"); ctxLogger != nil {
		if ctxLoggerTyped, ok := ctxLogger.(*zerolog.Logger); ok {
			logger = *ctxLoggerTyped
		}
	} else if config.Logger != nil {
		logger = *config.Logger
	}

	logger = logger.With().
		Str("input", config.InputExcelPath).
		Str("output", config.OutputPDFPath).
		Logger()

	logger.Info().Msg("starting Excel-to-PDF conversion")

	// Get absolute paths
	absInputPath, err := filepath.Abs(config.InputExcelPath)
	if err != nil {
		logger.Err(err).Msg("failed to get absolute input path")
		return util.Error("filepath.Abs(input)", err)
	}

	absOutputPath, err := filepath.Abs(config.OutputPDFPath)
	if err != nil {
		logger.Err(err).Msg("failed to get absolute output path")
		return util.Error("filepath.Abs(output)", err)
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(absOutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		logger.Err(err).Msg("failed to create output directory")
		return util.Error("os.MkdirAll", err)
	}

	// Copy input to a unique temp file so concurrent conversions of the same
	// source file don't collide on LibreOffice's output name (it derives the
	// PDF name from the input filename).
	inputExt := filepath.Ext(absInputPath)
	tempInput, err := os.CreateTemp(outputDir, "libre-convert-*"+inputExt)
	if err != nil {
		logger.Err(err).Msg("failed to create temp input file")
		return util.Error("os.CreateTemp", err)
	}
	tempInputPath := tempInput.Name()
	defer os.Remove(tempInputPath)

	srcFile, err := os.Open(absInputPath)
	if err != nil {
		tempInput.Close()
		logger.Err(err).Msg("failed to open input file")
		return util.Error("os.Open", err)
	}
	if _, err := io.Copy(tempInput, srcFile); err != nil {
		srcFile.Close()
		tempInput.Close()
		logger.Err(err).Msg("failed to copy input to temp file")
		return util.Error("io.Copy", err)
	}
	srcFile.Close()
	tempInput.Close()

	absWorkerProfileDir, err := filepath.Abs(workerProfileDir)
	if err != nil {
		logger.Err(err).Msg("failed to get absolute path for worker profile directory")
		return util.Error("filepath.Abs(workerProfileDir)", err)
	}

	userInstallURL := libreOfficeProfileURL(absWorkerProfileDir)
	args := []string{
		fmt.Sprintf("-env:UserInstallation=%s", userInstallURL),
		"--headless",
		"--convert-to", "pdf",
		"--outdir", outputDir,
		tempInputPath,
	}

	logger.Debug().
		Str("bin", l.libreOfficeBin).
		Strs("args", args).
		Msg("executing LibreOffice command")

	// Create command with context for timeout/cancellation support
	cmd := exec.CommandContext(ctx, l.libreOfficeBin, args...)

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Err(err).
			Str("output", string(output)).
			Msg("LibreOffice conversion failed")
		return util.Error("exec.Command", fmt.Errorf("LibreOffice failed: %w\nOutput: %s", err, string(output)))
	}

	logger.Debug().Str("output", string(output)).Msg("LibreOffice conversion output")

	// LibreOffice outputs "<tempName>.pdf" in the output directory
	tempBase := filepath.Base(tempInputPath)
	tempName := tempBase[:len(tempBase)-len(filepath.Ext(tempBase))]
	libreOutputPath := filepath.Join(outputDir, tempName+".pdf")
	defer os.Remove(libreOutputPath) // clean up intermediate PDF if rename succeeds or fails

	// Rename LibreOffice output to the desired path
	if libreOutputPath != absOutputPath {
		logger.Debug().
			Str("from", libreOutputPath).
			Str("to", absOutputPath).
			Msg("renaming output file")

		if err := os.Rename(libreOutputPath, absOutputPath); err != nil {
			logger.Err(err).Msg("failed to rename output file")
			return util.Error("os.Rename", err)
		}
	}

	// Verify output file exists
	if _, err := os.Stat(absOutputPath); os.IsNotExist(err) {
		logger.Error().Msg("output PDF file not created")
		return util.MsgError("executeConversion", "output PDF file not created")
	}

	logger.Info().Msg("conversion completed successfully")
	return nil
}

// Convert performs a synchronous, thread-safe Excel-to-PDF conversion
func (l *LibreExcel2PDF) Convert(ctx context.Context, config ExcelToPDFTaskConfig) *util.Result {
	// Validate config
	if res := config.Validate(); res != nil {
		return res.With("Validate")
	}

	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return util.MsgError("Convert", "LibreExcel2PDF not started - call StartUp() first")
	}
	l.mu.Unlock()

	// Create result channel
	resultChan := make(chan *util.Result, 1)

	task := &conversionTask{
		ctx:    ctx,
		config: config,
		result: resultChan,
	}

	// Submit task to queue (blocks if queue is full)
	select {
	case l.taskQueue <- task:
		// Task queued successfully
	case <-ctx.Done():
		return util.Error("Convert", ctx.Err())
	case <-l.ctx.Done():
		return util.MsgError("Convert", "LibreExcel2PDF is shutting down")
	}

	// Wait for result
	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return util.Error("Convert", ctx.Err())
	case <-l.ctx.Done():
		return util.MsgError("Convert", "LibreExcel2PDF was shut down")
	}
}

// Shutdown gracefully shuts down the converter, completing queued tasks
func (l *LibreExcel2PDF) Shutdown(ctx context.Context) {
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		l.logger.Warn().Msg("LibreExcel2PDF not started, nothing to shutdown")
		return
	}
	l.mu.Unlock()

	l.logger.Info().Msg("shutting down LibreExcel2PDF")

	// Close task queue to prevent new tasks
	close(l.taskQueue)

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		l.logger.Info().Msg("all workers finished gracefully")
	case <-ctx.Done():
		l.logger.Warn().Msg("shutdown timeout - cancelling remaining tasks")
		l.cancel()
		<-done
	}

	l.mu.Lock()
	l.started = false
	l.mu.Unlock()

	l.cleanupWorkerProfiles()

	l.logger.Info().Msg("LibreExcel2PDF shutdown complete")
}

// cleanupWorkerProfiles removes worker-specific profile directories
func (l *LibreExcel2PDF) cleanupWorkerProfiles() {
	for i := 0; i < l.maxConcurrent; i++ {
		workerProfileDir := filepath.Join(l.tempFolder, fmt.Sprintf("libreoffice-worker-%d", i))
		if err := os.RemoveAll(workerProfileDir); err != nil {
			l.logger.Warn().
				Err(err).
				Int("worker", i).
				Str("profile_dir", workerProfileDir).
				Msg("failed to clean up worker profile directory")
		} else {
			l.logger.Debug().
				Int("worker", i).
				Str("profile_dir", workerProfileDir).
				Msg("worker profile directory cleaned up")
		}
	}
}
