package cli

// Run executes wacli with the given arguments, including the output-signal and
// device-label setup the real binary performs in main().
func Run(args []string) error {
	configureOutputSignals()
	applyDeviceLabel()
	return execute(args)
}
