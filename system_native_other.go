//go:build !windows

package main

import "fmt"

func nrptRulePresent() bool             { return false }
func anyGatewayDomainRulePresent() bool { return false }
func setBitcoinDomainRuleElevated(bool) error {
	return fmt.Errorf(".bitcoin system resolution is only available on Windows")
}
func (a *app) startWindowsTray(string) {}
func stopWindowsTray()                 {}
