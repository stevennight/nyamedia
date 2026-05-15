package app

import provideriface "NyaMedia/internal/provider"

const scanIgnoreFileName = ".ignore"

type scanRuleDecision struct {
	Skip   bool
	Reason string
}

type scanRuleEvaluator struct{}

func newScanRuleEvaluator() scanRuleEvaluator {
	return scanRuleEvaluator{}
}

func (scanRuleEvaluator) EvaluateDirectory(_ provideriface.Entry, children []provideriface.Entry) scanRuleDecision {
	for _, child := range children {
		if !child.IsDir && child.Name == scanIgnoreFileName {
			return scanRuleDecision{Skip: true, Reason: "ignore_file"}
		}
	}
	return scanRuleDecision{}
}
