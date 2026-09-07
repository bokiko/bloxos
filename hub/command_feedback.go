package main

import (
	"strings"
	"time"
)

// These commands can terminate the process that would acknowledge completion.
// A successful socket write means only "sent", never "executed successfully".
func commandMayDisconnectAgent(osName, commandType, target string) bool {
	if osName != "linux" && osName != "windows" {
		return false
	}
	if commandType == "reboot" || commandType == "shutdown" {
		return true
	}
	if commandType != "restart_service" && commandType != "stop_service" {
		return false
	}
	if osName == "windows" {
		return strings.EqualFold(target, "BloxOSAgent")
	}
	return target == "bloxos-agent" || target == "bloxos-agent.service"
}

const commandSentNotice = "Request sent. Completion is not confirmed; check the machine's status."

// Briefly keep the response channel open so immediate permission/service
// errors are still shown, even for commands expected to sever the socket.
const disconnectCommandGrace = 2 * time.Second

func commandAcceptedNotice(commandType string) string {
	if commandType == "stop_service" {
		return "Stop request sent. Completion is not confirmed. If the agent stops, starting it again requires access outside BloxOS."
	}
	return commandSentNotice
}
