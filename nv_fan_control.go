// A simple tool that adjusts the fan speed of the GPU based on its temperature.
// Its designed for a custom cooler I have on my Nvidia Tesla P100 which is a 12v non-pwm fan.
// Author: Sam McLeod @sammcj
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Global Variables
var currentIndex int
var fanPath string
var fanSensitivity float64
var daemonMode bool
var pollInterval int
var listAllFans bool
var basePWM int
var maxPWM int
var maxSamples int = 40
var save bool
var load bool
var previousTemperature int
var temp bool
var threshold int
var logFile string
var logger *log.Logger
var maxTemp int
var previousPWM int
var debug bool
var offBelow int
var offSamples int
var idxOffSample int
var pwmMultiplier float64 = 1.0
var simpleFanSpeed bool = true
var gpuID int = 0
var virtualSensorPath string = "/sys/class/thermal/nvidiaVirtualSensor/temp"
var virtualSensor bool = false
var idlePowerSave bool = false // #TODO: this isn't working yet
var idlePowerSaveThreshold int = 5
var maxClockSpeed int = 0
var maxMemorySpeed int = 0
var idleClockSpeed int = 0
var idleMemorySpeed int = 0

// Set temperatureChanges to [maxSamples]int
var temperatureChanges = make([]int, maxSamples)

// Constants
const (
	configPath = "~/.config/nv_fan_control"
)

type Config struct {
	FanPath                string  `json:"fan_path"`
	FanSensitivity         float64 `json:"fan_sensitivity"`
	DaemonMode             bool    `json:"daemon_mode"`
	PollInterval           int     `json:"poll_interval"`
	BasePWM                int     `json:"base_pwm"`
	MaxPWM                 int     `json:"max_pwm"`
	MaxSamples             int     `json:"max_samples"`
	MaxTemp                int     `json:"max_temp"`
	OffBelow               int     `json:"off_below"`
	Threshold              int     `json:"threshold"`
	LogFile                string  `json:"log_file"`
	Debug                  bool    `json:"debug"`
	OffSamples             int     `json:"off_samples"`
	SimpleFanSpeed         bool    `json:"simple_fan_speed"`
	GPUID                  int     `json:"gpu_id"`
	VirtualSensor          bool    `json:"virtual_sensor"`
	IdlePowerSave          bool    `json:"idle_power_save"`
	IdlePowerSaveThreshold int     `json:"idle_power_save_threshold"`
}

func init() {
	// Initialize the logger
	logger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)

	flag.StringVar(&fanPath, "fanpath", "/sys/class/hwmon/hwmon5/pwm3", "Path to the PWM fan control")
	flag.Float64Var(&fanSensitivity, "sensitivity", 1.25, "Higher means slower initial response to temp changes.")
	flag.BoolVar(&daemonMode, "daemon", false, "Run as a background daemon")
	flag.IntVar(&pollInterval, "interval", 3, "Polling interval in seconds")
	flag.BoolVar(&listAllFans, "list", false, "List all controllable fans")
	flag.IntVar(&basePWM, "basePWM", 42, "Baseline PWM value for the fan, it is off below this")
	flag.IntVar(&maxPWM, "maxPWM", 250, "Maximum PWM value for the fan")
	flag.IntVar(&offBelow, "offBelow", 34, "Turn the fan off below this temperature (in degrees C)")
	flag.IntVar(&offSamples, "offSamples", 10, "Number of samples to wait before turning the fan off")
	flag.BoolVar(&save, "save", false, "Save to the config file on shutdown (~/.config/nv_fan_control)")
	flag.BoolVar(&load, "load", false, "Load config file (~/.config/nv_fan_control)")
	flag.BoolVar(&temp, "temp", false, "Print the current GPU temperature and exit")
	flag.IntVar(&threshold, "threshold", 60, "Temperature threshold to move linear response (in degrees C))")
	flag.IntVar(&maxTemp, "maxTemp", 76, "Maximum operating temperature, fan at 100%")
	flag.StringVar(&logFile, "log", "", "File to log to (leave blank to log to journalctl)")
	flag.IntVar(&maxSamples, "MaxSamples", 40, "Number of samples to log for the moving average information")
	flag.BoolVar(&debug, "debug", false, "Enable debug logging")
	flag.BoolVar(&simpleFanSpeed, "simpleFanSpeed", true, "Use a simple fan speed algorithm")
	flag.IntVar(&gpuID, "gpuID", 0, "GPU ID to control")
	flag.BoolVar(&virtualSensor, "virtualSensor", false, "Use the virtual sensor instead of the GPU temperature")
	// flag.BoolVar(&idlePowerSave, "idlePowerSave", true, "Reduce speed when idle (even when app is loaded, but not processing)") // #TODO: this isn't working yet
	// flag.IntVar(&idlePowerSaveThreshold, "idlePowerSaveThreshold", 5, "GPU utilisation threshold for idle power saving")

	flag.Parse()

	if temp {
		load = true
		save = false
		fmt.Println(getGPUTemperature())
		os.Exit(0)
	}

	originalFanPath := fanPath
	originalFanSensitivity := fanSensitivity
	originalDaemonMode := daemonMode
	originalPollInterval := pollInterval
	originalBasePWM := basePWM
	originalMaxPWM := maxPWM
	originalThreshold := threshold
	originalLogFile := logFile
	originalMaxTemp := maxTemp
	originalMaxSamples := maxSamples
	originalOffBelow := offBelow
	originalOffSamples := offSamples
	originalSimpleFanSpeed := simpleFanSpeed
	originalGPUID := gpuID
	originalVirtualSensor := virtualSensor
	originalIdlePowerSave := idlePowerSave
	originalIdlePowerSaveThreshold := idlePowerSaveThreshold

	if load && os.IsExist(checkConfig()) {
		loadConfig()
	}

	if fanPath != originalFanPath {
		fanPath = originalFanPath
	}
	if fanSensitivity != originalFanSensitivity {
		fanSensitivity = originalFanSensitivity
	}
	if daemonMode != originalDaemonMode {
		daemonMode = originalDaemonMode
	}
	if pollInterval != originalPollInterval {
		pollInterval = originalPollInterval
	}
	if basePWM != originalBasePWM {
		basePWM = originalBasePWM
	}
	if maxPWM != originalMaxPWM {
		maxPWM = originalMaxPWM
	}
	if threshold != originalThreshold {
		threshold = originalThreshold
	}
	if logFile != originalLogFile {
		logFile = originalLogFile
	}
	if maxTemp != originalMaxTemp {
		maxTemp = originalMaxTemp
	}
	if maxSamples != originalMaxSamples {
		maxSamples = originalMaxSamples
	}
	if offBelow != originalOffBelow {
		offBelow = originalOffBelow
	}
	if offSamples != originalOffSamples {
		offSamples = originalOffSamples
	}
	if simpleFanSpeed != originalSimpleFanSpeed {
		simpleFanSpeed = originalSimpleFanSpeed
	}
	if gpuID != originalGPUID {
		gpuID = originalGPUID
	}
	if virtualSensor != originalVirtualSensor {
		virtualSensor = originalVirtualSensor
	}
	if idlePowerSave != originalIdlePowerSave {
		idlePowerSave = originalIdlePowerSave
	}
	if idlePowerSaveThreshold != originalIdlePowerSaveThreshold {
		idlePowerSaveThreshold = originalIdlePowerSaveThreshold
	}

	if save {
		defer saveConfig()
	}

	// Setup logging
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		logger = log.New(f, "", log.LstdFlags)
	} else {
		syslogger, err := syslog.New(syslog.LOG_NOTICE, "nv_fan_control")
		if err != nil {
			log.Fatal(err)
		}
		logger = log.New(syslogger, "", 0)
	}

	// Check/set the fan to PWM mode
	checkPwmMode()
}

func expandHome(path string) string {
	home, _ := os.UserHomeDir()
	return strings.Replace(path, "~", home, 1)
}

func checkConfig() error {
	_, err := os.Stat(expandHome(configPath))
	return err
}

func loadConfig() {
	data, err := os.ReadFile(expandHome(configPath))
	if err != nil {
		logger.Println("Failed to read config file:", err)
		return
	}

	var cfg Config
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		logger.Println("Failed to parse config file:", err)
		return
	}

	fanPath = cfg.FanPath
	fanSensitivity = cfg.FanSensitivity
	daemonMode = cfg.DaemonMode
	pollInterval = cfg.PollInterval
	basePWM = cfg.BasePWM
	maxPWM = cfg.MaxPWM
	threshold = cfg.Threshold
	logFile = cfg.LogFile
	maxTemp = cfg.MaxTemp
	offSamples = cfg.OffSamples
	offBelow = cfg.OffBelow
	simpleFanSpeed = cfg.SimpleFanSpeed
	gpuID = cfg.GPUID
	virtualSensor = cfg.VirtualSensor
	idlePowerSave = cfg.IdlePowerSave
	idlePowerSaveThreshold = cfg.IdlePowerSaveThreshold

	logger.Println("Found and loaded config with values:", cfg)
}

func saveConfig() {
	logger.Println("Saving current settings to config file...")

	cfg := Config{
		FanPath:                fanPath,
		FanSensitivity:         fanSensitivity,
		DaemonMode:             daemonMode,
		PollInterval:           pollInterval,
		BasePWM:                basePWM,
		MaxPWM:                 maxPWM,
		MaxTemp:                maxTemp,
		Threshold:              threshold,
		LogFile:                logFile,
		OffSamples:             offSamples,
		OffBelow:               offBelow,
		SimpleFanSpeed:         simpleFanSpeed,
		GPUID:                  gpuID,
		VirtualSensor:          virtualSensor,
		IdlePowerSave:          idlePowerSave,
		IdlePowerSaveThreshold: idlePowerSaveThreshold,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		logger.Println("Failed to serialize config:", err)
		return
	}

	err = os.WriteFile(expandHome(configPath), data, 0644)
	if err != nil {
		logger.Println("Failed to save config:", err)
	} else {
		logger.Println("Config saved successfully. The program will run with these settings next time.")
	}
}

func getGPUTemperature() int {
	output, err := exec.Command("nvidia-smi", "--query-gpu=temperature.gpu", "--format=csv,noheader,nounits").Output()

	if err != nil {
		logger.Println("Failed to get GPU temperature:", err)
		os.Exit(1)
	}

	trimmedOutput := strings.TrimSuffix(string(output), "\n")
	temperature, err := strconv.Atoi(trimmedOutput)
	if err != nil {
		logger.Println("Failed to parse GPU temperature:", err)
		os.Exit(1)
	}

	return temperature
}

func fanControl(gpuID int, virtualSensorPath string, debug bool, pollinterval int) {
	// At ever pollinterval, read the temperature and write it to a virtual sensor

	// Get the temperature
	temperature := getGPUTemperature()

	// A special temp output for reading by fan-control
	sensorMDegrees := temperature * 1000

	if debug {
		// Get the current time
		currentTime := time.Now().Format(time.RFC3339)
		// Format the output
		println("%s GPU %s has temperature %dc", currentTime, gpuID, temperature, sensorMDegrees)
	}

	// Output the temperature to a virtual sensor
	err := os.WriteFile(virtualSensorPath, []byte(strconv.Itoa(sensorMDegrees)), 0644)
	if err != nil {
		logger.Println("Failed to write to virtual sensor:", err)
		os.Exit(1)
	}

	// Wait for the next poll
	time.Sleep(time.Duration(pollinterval) * time.Second)

	// Call the function again
	fanControl(gpuID, virtualSensorPath, debug, pollinterval)
}

func checkPwmMode() {
	// check that $fanPath_enable is set to 1, if not set it to 1
	// this is to ensure that the fan is controlled by pwm and not by the gpu temperature

	// read the file
	data, err := os.ReadFile(fanPath + "_enable")
	if err != nil {
		logger.Println("Failed to read fan enable file:", err)
		os.Exit(1)
	}

	// check if the file is set to 1
	if string(data) != "1" {

		// set the sysfs object to 1 to enable software pwm control
		err = os.WriteFile(fanPath+"_enable", []byte("1"), 0644)
		println("Setting fan enable file to 1 to enable software pwm control")
		if err != nil {
			logger.Println("Failed to set fan enable file:", err)
			os.Exit(1)
		}
	}
}

func simpleFan(temperature int, basePWM int, maxPWM int, fanSensitivity float64) int {
	// A super simple fan speed algorithm that linearly scales the PWM value from basePWM to maxPWM based on the temperature
	// Ensure the temperature is within the range of 0-100
	if temperature < 0 {
		temperature = 0
	} else if temperature > 100 {
		temperature = 100
	}

	// Ensure the basePWM and maxPWM are within the range of 0-255
	if basePWM < 0 {
		basePWM = 0
	} else if basePWM > 255 {
		basePWM = 255
	}

	if maxPWM < 0 {
		maxPWM = 0
	} else if maxPWM > 255 {
		maxPWM = 255
	}

	// Apply the sensitivity curve to the temperature
	scaledTemp := math.Pow(float64(temperature)/100, fanSensitivity) * 100

	// Linearly scale the PWM value from basePWM to maxPWM based on the scaled temperature
	scaledPWM := basePWM + int((float64(maxPWM-basePWM)*scaledTemp)/100)

	return scaledPWM
}

func setFanSpeed(pwm int) {
	// If the current fan speed is < basePWM give the fan an extra kick on top of basePWM to get it going
	if pwm > basePWM && previousPWM < basePWM {
		pwm = basePWM + 25
		time.Sleep(750 * time.Millisecond)
	}
	// write the pwm the file
	err := os.WriteFile(fanPath, []byte(strconv.Itoa(pwm)), 0644)
	if err != nil {
		logger.Println("Failed to set fan speed:", err)
	}
	if debug {
		logger.SetPrefix("DEBUG: ")
		logger.Println("Ran:", fanPath, []byte(strconv.Itoa(pwm)))
	}

	// check that the file was written correctly (read it to an int and compare)
	outputByte, err := os.ReadFile(fanPath)
	if err != nil {
		logger.Println("Failed to read fan speed:", err)
	}
	// take the output bytes and read them as an int
	outputInt, err := strconv.Atoi(strings.TrimSpace(string(outputByte)))

	// compare the output to the input
	if outputInt != pwm {
		logger.Println("Failed to set fan speed: output does not match input")
	}
	if debug {
		logger.SetPrefix("DEBUG: ")
		logger.Println("Read:", fanPath, outputInt)
	}

	previousPWM = pwm
}

// This function checks if the GPU is currently under heavy computation or CUDA load.
func GPUUtilisation() int {
	// This command checks for utilisation.
	output, err := exec.Command("nvidia-smi", "--query-gpu", "utilization.gpu", "--format=csv,nounits,noheader").Output()
	if err != nil {
		logger.Println("Failed to check GPU computational load:", err)
		utilisation := 100
		return utilisation
	} else {
		utilisation, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			logger.Println("Failed to parse GPU computational load:", err)
			utilisation := 100
			return utilisation
		} else {
			return utilisation
		}
	}
}

// Checks for other running instances of this application.
func isAlreadyRunning() bool {
	processList, _ := exec.Command("pgrep", "-f", "nv_fan_control").Output()

	// remove the current process from the list
	processList = bytes.ReplaceAll(processList, []byte(strconv.Itoa(os.Getpid())), []byte(""))

	processes := strings.Split(strings.TrimSpace(string(processList)), "\n")
	return len(processes) > 1
}

func dataLog(previousTemperature int, previousPWM int, temperature int, pwm int) {
	if previousTemperature > 0 && previousPWM > 0 {
		tempChange := temperature - previousTemperature
		pwmChange := pwm - previousPWM

		if tempChange > 0 || pwmChange > 0 {
			// Log the relation between the PWM and temperature changes
			logger.Printf("For PWM change of %d, the temperature change was: %d\n", pwmChange, tempChange)
		}

		// only run this if it's not the first few runs
		if currentIndex > 1 {
			// Check if the temperature change was significant but the PWM change was not
			if math.Abs(float64(pwmChange)) > 10 && math.Abs(float64(tempChange)) < 1 {
				logger.Println("Alert: Significant (>=10) PWM change did not affect temperature.")
			}
			currentIndex++
		} else {
			currentIndex++
		}

		// Storing temperature change
		if currentIndex < maxSamples {
			temperatureChanges[currentIndex] = temperature
			currentIndex++
		} else {
			// Output the summary
			dataSummary()
			// Shift all items to the left and add newTemp to the end
			copy(temperatureChanges[:], temperatureChanges[1:])
			temperatureChanges[maxSamples-1] = temperature
		}
	}
}

func dataSummary() {
	// Output all data from the array
	logger.Println("Temperature changes:", temperatureChanges)

	// Calculate the average temperature change
	var sum int
	for _, temp := range temperatureChanges {
		sum += temp
	}
	averageTempChange := sum / maxSamples

	// Log the average temperature change
	logger.Println("Average temperature change:", averageTempChange)

	// Calculate the average PWM change
	var sumPWM int
	for i := 1; i < maxSamples; i++ {
		sumPWM += temperatureChanges[i] - temperatureChanges[i-1]
	}
	averagePWMChange := sumPWM / (maxSamples - 1)

	// Log the average PWM change
	logger.Println("Average PWM change:", averagePWMChange)

	// Calculate the average temperature change per PWM change
	averageTempChangePerPWMChange := float64(averageTempChange) / float64(averagePWMChange)

	// Log the average temperature change per PWM change
	logger.Println("Average temperature change per PWM change:", averageTempChangePerPWMChange)
}

func getClockSpeed() (int, int, int, int) {
	output, err := exec.Command("nvidia-smi", "--format=csv,nounits", "--query-supported-clocks", "gr,mem").Output()
	if err != nil {
		logger.Println("Failed to get GPU clock speeds:", err)
	}
	// this outputs:
	// graphics [MHz], memory [MHz]
	// 1328, 715
	// 1316, 715
	// ...
	// 556, 715
	// 544, 715

	// Split the output into lines
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	// Remove The first line, any empty lines and whitespace
	lines = lines[1:]
	lines = lines[:len(lines)-1]
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}

	// Create an array of graphics and memory speeds
	graphicsSpeeds := make([]int, len(lines))
	memorySpeeds := make([]int, len(lines))
	// Find the largest number in each array
	for i, line := range lines {
		// Split the line into graphics and memory speeds
		speeds := strings.Split(line, ", ")
		// Convert the speeds to ints
		graphicsSpeeds[i], err = strconv.Atoi(speeds[0])
		if err != nil {
			logger.Println("Failed to parse GPU graphics clock speed:", err)
		}
		memorySpeeds[i], err = strconv.Atoi(speeds[1])
		if err != nil {
			logger.Println("Failed to parse GPU memory clock speed:", err)
		}
	}

	// Find the max graphics and memory speeds
	maxGraphicsSpeed := 0
	maxMemorySpeed := 0

	for _, speed := range graphicsSpeeds {
		if speed > maxGraphicsSpeed {
			maxGraphicsSpeed = speed
		}

	}
	for _, speed := range memorySpeeds {
		if speed > maxMemorySpeed {
			maxMemorySpeed = speed
		}
	}

	// Find the idle graphics and memory speeds
	idleGraphicsSpeed := 0
	idleMemorySpeed := 0

	for _, speed := range graphicsSpeeds {
		if speed < idleGraphicsSpeed || idleGraphicsSpeed == 0 {
			idleGraphicsSpeed = speed
		}
	}
	for _, speed := range memorySpeeds {
		if speed < idleMemorySpeed || idleMemorySpeed == 0 {
			idleMemorySpeed = speed
		}
	}

	// Print the clock speeds
	logger.Println("Max graphics clock speed:", maxGraphicsSpeed)
	logger.Println("Max memory clock speed:", maxMemorySpeed)

	logger.Println("Idle graphics clock speed:", idleGraphicsSpeed)
	logger.Println("Idle memory clock speed:", idleMemorySpeed)

	// return the max and idle clock speeds
	return maxGraphicsSpeed, maxMemorySpeed, idleGraphicsSpeed, idleMemorySpeed
}

func setClockSpeed(int, int, int, int) {
	// If the GPU is less than 5% utilised, reduce it's clock and memory speeds to save power
	// e.g. nvidia-smi -ac 715,544 -i 0
	// if it's more than 5% utilised, set it to the max clock and memory speeds
	// e.g. nvidia-smi -ac 715,1328 -i 0

	// If maxClockSpeed, maxMemorySpeed, idleClockSpeed, idleMemorySpeed are not set, get them
	if maxClockSpeed == 0 || maxMemorySpeed == 0 || idleClockSpeed == 0 || idleMemorySpeed == 0 {
		maxClockSpeed, maxMemorySpeed, idleClockSpeed, idleMemorySpeed = getClockSpeed()
	}

	// Get the current GPU utilisation
	utilisation := GPUUtilisation()

	// If the GPU is less than idlePowerSaveThreshold% utilised, reduce it's clock and memory speeds to save power
	if utilisation < idlePowerSaveThreshold {
		// Set the clock and memory speeds
		cmd := exec.Command("nvidia-smi", "-ac", strconv.Itoa(idleClockSpeed)+","+strconv.Itoa(idleMemorySpeed), "-i", strconv.Itoa(gpuID))
		err := cmd.Run()
		if err != nil {
			logger.Println("Failed to set clock and memory speeds:", err)
		}
		logger.Println("Set clock and memory speeds to idle speeds (", idleClockSpeed, ",", idleMemorySpeed, ")")
	} else {
		// Set the clock and memory speeds
		cmd := exec.Command("nvidia-smi", "-ac", strconv.Itoa(maxClockSpeed)+","+strconv.Itoa(maxMemorySpeed), "-i", strconv.Itoa(gpuID))
		err := cmd.Run()
		if err != nil {
			logger.Println("Failed to set clock and memory speeds:", err)
		}
		logger.Println("Set clock and memory speeds to max speeds (", maxClockSpeed, ",", maxMemorySpeed, ")")
	}
}

func main() {
	if listAllFans {
		// List all fans and exit
		os.Exit(0)
	}

	if !daemonMode && isAlreadyRunning() {
		logger.Println("Another instance of nv_fan_control is running.")
		fmt.Println("Would you like to:")
		fmt.Println("1. Stop the daemon?")
		fmt.Println("2. Tail the existing log file?")
		fmt.Println("3. Exit?")
		var choice int
		fmt.Scan(&choice)

		switch choice {
		case 1:
			// Stop the daemon (this implementation might need to be more robust)
			exec.Command("systemctl", "stop", "nv_fan_control").Run()
		case 2:
			// Assuming you have a command to tail the log. Modify as necessary.
			exec.Command("tail", "-f", "/var/log/nv_fan_control.log").Run()
			os.Exit(0)
		case 3:
			os.Exit(0)
		default:
			fmt.Println("Invalid choice.")
			os.Exit(1)
		}
	}

	loadConfig()

	// If running in virtualSensor mode simply call the fanControl function which will run indefinitely
	if virtualSensor {
		fanControl(
			gpuID,
			virtualSensorPath,
			debug,
			pollInterval,
		)
	}

	logFile := "/var/log/nv_fan_control.log"
	// If it already exists, rename it to .1 and create a new one
	if _, err := os.Stat(logFile); err == nil {
		os.Rename(logFile, logFile+".1")
	}

	logf, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer logf.Close()

	// Reinitialize the logger
	if logFile != "" {
		logger = log.New(io.MultiWriter(os.Stdout, logf), "", log.LstdFlags)
		logger.Println("Logging started")
	} else {
		syslogger, err := syslog.New(syslog.LOG_NOTICE, "nv_fan_control")
		if err != nil {
			log.Fatal(err)
		}
		logger = log.New(io.MultiWriter(os.Stdout, syslogger), "", 0)
		logger.Println("Logging started")
	}

	// Log out the current settings
	logger.Println("Current settings:", Config{
		FanPath:        fanPath,
		FanSensitivity: fanSensitivity,
		DaemonMode:     daemonMode,
		PollInterval:   pollInterval,
		BasePWM:        basePWM,
		MaxPWM:         maxPWM,
		MaxTemp:        maxTemp,
		MaxSamples:     maxSamples,
		Threshold:      threshold,
		LogFile:        logFile,
		OffBelow:       offBelow,
		OffSamples:     offSamples,
		SimpleFanSpeed: simpleFanSpeed,
		GPUID:          gpuID,
		VirtualSensor:  virtualSensor,
	})

	if daemonMode {
		// Start in daemon mode
		if _, err := os.Stat("/.dockerenv"); err == nil {
			// We're inside a Docker container, don't daemonize
			logger.Println("Running in Docker container, won't daemonize")
		} else if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(os.Getppid()), "cmdline")); err == nil {
			// We're not the top process, fork off a child
			logger.Println("Not top process, forking")
			args := os.Args[1:]

			// Ensure only one -daemon flag
			newArgs := []string{}
			for _, arg := range args {
				if arg != "-daemon" {
					newArgs = append(newArgs, arg)
					logger = log.New(io.MultiWriter(logf, os.Stdout), "", log.LstdFlags)

				}

			}
			newArgs = append(newArgs, "-daemon")

			cmd := exec.Command(os.Args[0], newArgs...)
			cmd.Start()
			logger.Println("Forked child, exiting")
			logger.Println("Child PID:", cmd.Process.Pid)
			os.Exit(0)
		}
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range signalChan {
			logger.Println("Caught an interrupt, stopping fan control")
			// Output the summary
			dataSummary()

			// check if we're running computations
			if GPUUtilisation() > 1 {
				logger.Println("Computations are running, exiting with fans at current speed")
				os.Exit(0)
			}
			setFanSpeed(0) // Let the system control the fan
			os.Exit(0)
		}
	}()

	for {
		temperature := getGPUTemperature()

		var pwm int

		// If previousTemperature is not between 0 and maxTemp, set the fans to maxPWM (for safety)
		if previousTemperature < 0 || previousTemperature > maxTemp {
			pwm = maxPWM
		}

		// if there's no change, don't do anything
		if temperature == previousTemperature {
			time.Sleep(time.Duration(pollInterval) * time.Second)
			continue
		}

		if debug {
			println("Condition met: temperature <= threshold %d <= %d", temperature, threshold)
		}

		// If the temperature is below offBelow and the utilisation is below 5%, turn the fan off
		if temperature < offBelow && (GPUUtilisation() < 5) {
			if debug {
				println("Condition met: temperature < offBelow && (GPUUtilisation() < 5) %d < %d && (%d < 5)", temperature, offBelow, GPUUtilisation())
			}
			// If the temperature has been below offBelow for more than 10 samples, turn the fan off
			if idxOffSample < offSamples {
				idxOffSample++
				logger.Printf("GPU Temp: %d. Below %d and utilisation is below 5%%, [Not turning off, until cycle %d >= %d]\n", temperature, offBelow, idxOffSample, offSamples)
			} else {
				pwm = 0
				logger.Printf("GPU Temp: %d. Below %d and utilisation is below 5%%, [Turning off]\n", temperature, offBelow)
			}
		} else { // If the temperature is above offBelow or the utilisation is above 5%, turn the fan on
			if debug {
				println("Condition met: temperature (>= offBelow || GPUUtilisation() >= 5 &&) && < threshold %d (>= %d || %d >= 5) && < %d", temperature, offBelow, GPUUtilisation(), threshold)
			}

			// If run with simpleFanSpeed true, use the simple fan speed algorithm
			if simpleFanSpeed {
				pwm = simpleFan(getGPUTemperature(), basePWM, maxPWM, fanSensitivity)
			} else {

				// Calculate the PWM (pwm) based on the temp between offbelow (temp) and threshold (temp)
				if temperature <= threshold {
					maxPWMFloat := float64(maxPWM)
					basePWMFloat := float64(basePWM)
					pwm = basePWM + int((maxPWMFloat-basePWMFloat)*(float64(temperature-offBelow)/float64(threshold-offBelow)))
				} else {
					if debug {
						println("Condition met: temperature > threshold", temperature, ">", threshold)
					}
					multiplier := pwmMultiplier
					pwm = basePWM + int(float64(maxPWM-basePWM)*(float64(temperature-threshold)/float64(100-threshold))*multiplier)
				}
			}

			logger.Printf("GPU Temp: %d. Utilisation: %d%%, [Calculated PWM: %d]\n", temperature, GPUUtilisation(), pwm)
		}
		// Update the fan PWM
		setFanSpeed(pwm)
		// Store the current temperature values
		previousTemperature = temperature

		// // in a sub-routine, check if the GPU is idle and set the clock speeds accordingly
		// if idlePowerSave {
		// 	go setClockSpeed(maxClockSpeed, maxMemorySpeed, idleClockSpeed, idleMemorySpeed)
		// }

		// Log the data
		go dataLog(previousTemperature, previousPWM, temperature, pwm)

		time.Sleep(time.Duration(pollInterval) * time.Second)
	}
}
