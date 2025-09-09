package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration structure
type Config struct {
	Tabs map[string]map[string]Command `yaml:"tabs"`
}

// Command represents a command to execute
type Command struct {
	Exec string   `yaml:"exec"`
	Args []string `yaml:"args"`
}

// ProcessState tracks the state of a running process
type ProcessState struct {
	Cmd     *exec.Cmd
	Running bool
	Output  []string
	Mutex   sync.Mutex
}

// TabData represents UI tab data
type TabData struct {
	Title string
	Rows  []RowData
}

// RowData represents UI row data
type RowData struct {
	Title       string
	Description string
	Command     Command
}

// Global variables to track process states
var processStates = make(map[string]*ProcessState)
var config Config

func main() {
	// Load configuration from YAML file
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	err := loadConfig(configFile)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Create the application
	myApp := app.New()
	myWindow := myApp.NewWindow("Command Runner")
	myWindow.Resize(fyne.NewSize(600, 480))

	// Convert config to UI data
	tabsData := convertConfigToTabsData(config)

	// Create tabs with icons
	tabs := container.NewAppTabs()

	for _, tabData := range tabsData {
		tabContent := createTabContent(tabData)
		tabs.Append(container.NewTabItem(tabData.Title, tabContent))
	}

	myWindow.SetContent(tabs)
	myWindow.ShowAndRun()
}

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	return nil
}

func convertConfigToTabsData(config Config) []TabData {
	var tabsData []TabData

	for tabName, commands := range config.Tabs {
		var rows []RowData
		for cmdName, command := range commands {
			rows = append(rows, RowData{
				Title:       cmdName,
				Description: fmt.Sprintf("%s %v", command.Exec, command.Args),
				Command:     command,
			})
		}
		tabsData = append(tabsData, TabData{
			Title: tabName,
			Rows:  rows,
		})
	}

	return tabsData
}

func createTabContent(tabData TabData) fyne.CanvasObject {
	listContainer := container.NewVBox()

	for _, row := range tabData.Rows {
		rowWidget := createRowWidget(row)
		listContainer.Add(rowWidget)
	}

	listContainer.Add(layout.NewSpacer())
	return container.NewVScroll(listContainer)
}

func createRowWidget(row RowData) *fyne.Container {
	// Create a unique key for this process
	processKey := fmt.Sprintf("%s-%s", row.Title, row.Description)

	// Main title
	titleLabel := widget.NewLabel(row.Title)
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Description
	descLabel := widget.NewLabel(row.Description)
	descLabel.TextStyle = fyne.TextStyle{Italic: true}

	// Status label
	statusLabel := widget.NewLabel("Stopped")
	statusLabel.Alignment = fyne.TextAlignTrailing

	// Run/Stop button
	actionButton := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), nil)
	actionButton.Importance = widget.SuccessImportance

	// Logs button
	logsButton := widget.NewButtonWithIcon("Logs", theme.DocumentIcon(), nil)
	logsButton.Importance = widget.MediumImportance
	logsButton.Hide() // Initially hidden

	// Update button state based on process state
	updateButtonState := func() {
		if state, exists := processStates[processKey]; exists && state.Running {
			actionButton.SetIcon(theme.MediaStopIcon())
			actionButton.Text = "" // Clear text to show only icon
			statusLabel.SetText("Running")
			logsButton.Show()
		} else {
			actionButton.SetIcon(theme.MediaPlayIcon())
			actionButton.Text = "" // Clear text to show only icon
			statusLabel.SetText("Stopped")
			logsButton.Hide()
		}
		actionButton.Refresh()
		logsButton.Refresh()
		statusLabel.Refresh()
	}

	// Set up action button click handler
	actionButton.OnTapped = func() {
		if state, exists := processStates[processKey]; exists && state.Running {
			// Stop the process
			state.Mutex.Lock()
			if state.Cmd != nil && state.Cmd.Process != nil {
				state.Cmd.Process.Kill()
			}
			state.Running = false
			state.Mutex.Unlock()
			fyne.Do(updateButtonState)
		} else {
			// Start the process
			cmd := exec.Command(row.Command.Exec, row.Command.Args...)

			// Create pipes for capturing output
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Printf("Error creating stdout pipe: %v\n", err)
				return
			}

			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				fmt.Printf("Error creating stderr pipe: %v\n", err)
				return
			}

			// Initialize process state
			processStates[processKey] = &ProcessState{
				Cmd:     cmd,
				Running: true,
				Output:  []string{},
			}

			// Start the process
			err = cmd.Start()
			if err != nil {
				fmt.Printf("Error starting process: %v\n", err)
				processStates[processKey].Running = false
				processStates[processKey].Output = append(processStates[processKey].Output, fmt.Sprintf("Error: %v", err))
				return
			}

			fyne.Do(updateButtonState)

			// Capture output in goroutines
			go captureOutput(stdoutPipe, processKey, false)
			go captureOutput(stderrPipe, processKey, true)

			// Wait for process completion in goroutine
			go func() {
				err := cmd.Wait()

				state, exists := processStates[processKey]
				if !exists {
					return
				}

				state.Mutex.Lock()
				state.Running = false
				if err != nil {
					state.Output = append(state.Output, fmt.Sprintf("Process exited with error: %v", err))
				} else {
					state.Output = append(state.Output, "Process completed successfully")
				}
				state.Mutex.Unlock()

				// Update UI by scheduling on the main thread
				// Fyne handles this automatically through widget refreshes
				fyne.Do(updateButtonState)
			}()
		}
	}

	// Set up logs button click handler
	logsButton.OnTapped = func() {
		if state, exists := processStates[processKey]; exists {
			state.Mutex.Lock()
			output := make([]string, len(state.Output))
			copy(output, state.Output)
			state.Mutex.Unlock()
			showLogsDialog(row.Title, output)
		}
	}

	// Initial button state
	fyne.Do(updateButtonState)

	// Vertical container for text content
	textContent := container.NewVBox(
		titleLabel,
		descLabel,
	)

	// Horizontal container for the entire row
	mainRow := container.NewHBox(
		textContent,
		layout.NewSpacer(),
		statusLabel,
		container.NewPadded(logsButton),
		container.NewPadded(actionButton),
	)

	// Add padding and border
	paddedRow := container.NewPadded(mainRow)
	borderedRow := container.NewBorder(
		nil,
		widget.NewSeparator(),
		nil,
		nil,
		paddedRow,
	)

	return borderedRow
}

func captureOutput(reader io.ReadCloser, processKey string, isStderr bool) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		state, exists := processStates[processKey]
		if !exists {
			return
		}

		state.Mutex.Lock()
		prefix := "OUT: "
		if isStderr {
			prefix = "ERR: "
		}
		state.Output = append(state.Output, prefix+line)
		state.Mutex.Unlock()
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading output: %v\n", err)
	}
}

func showLogsDialog(title string, output []string) {
	// Create a dialog to show logs
	logText := ""
	for _, line := range output {
		logText += line + "\n"
	}

	// Create a multi-line text widget for logs
	logWidget := widget.NewMultiLineEntry()
	logWidget.SetText(logText)
	logWidget.Wrapping = fyne.TextWrapWord
	logWidget.Resize(fyne.NewSize(600, 400))

	// Create a container with scroll
	scrollContainer := container.NewVScroll(logWidget)
	scrollContainer.SetMinSize(fyne.NewSize(600, 400))

	// Get the main window
	mainWindow := fyne.CurrentApp().Driver().AllWindows()[0]

	// Show custom dialog
	dialog.ShowCustom(title, "Close", scrollContainer, mainWindow)
}

// Helper function to get absolute path
func getAbsolutePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(path)
}
