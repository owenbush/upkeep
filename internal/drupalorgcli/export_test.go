package drupalorgcli

// resetGate forgets the per-process version verdict, so each test can point
// UPKEEP_DRUPALORG_BIN at its own stub.
func resetGate() { gate = &versionGate{} }
