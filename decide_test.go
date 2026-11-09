package jev

func decide(call toolCall, answer callAnswer, keepThreshold float64) decision {
	return decideWith(call, answer, thresholds{call: keepThreshold, result: keepThreshold})
}
