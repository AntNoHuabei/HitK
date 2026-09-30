package companion

type ConversationEffect struct {
	Target        MetricID `json:"target"`
	RatePerHour   float64  `json:"ratePerHour,omitempty"`
	BaselineDelta float64  `json:"baselineDelta,omitempty"`
	RecoveryDelta float64  `json:"recoveryDelta,omitempty"`
	DurationMin   int      `json:"durationMinutes"`
	Curve         string   `json:"curve,omitempty"`
}

type ConversationMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type ConversationAnalysisInput struct {
	Profile   Profile
	State     State
	User      string
	Assistant string
	Recent    []ConversationMessage
}

type ConversationOutcome struct {
	Signals      []string             `json:"signals"`
	Intensity    float64              `json:"intensity"`
	SceneStage   string               `json:"sceneStage"`
	ConsentState string               `json:"consentState"`
	Impulses     map[MetricID]float64 `json:"impulses"`
	TraitChanges map[TraitID]float64  `json:"traitChanges"`
	Effects      []ConversationEffect `json:"effects"`
	Reason       string               `json:"reason"`
}

var validConversationSignals = map[string]bool{
	"explicit": true, "intimate": true, "affection": true, "support": true,
	"vulnerable": true, "conflict": true, "apology": true, "humor": true,
	"boundary": true, "roleplay_resistance": true, "aftercare": true,
}

var validSceneStages = map[string]bool{
	"none": true, "flirt": true, "active": true, "climax": true, "aftercare": true, "stopped": true,
}

var validConsentStates = map[string]bool{
	"not_applicable": true, "affirmed": true, "roleplay": true, "ambiguous": true, "withdrawn": true,
}

var conversationMetricCap = map[MetricID]float64{
	MetricVitality:           25,
	MetricHunger:             25,
	MetricFatigue:            30,
	MetricSleepPressure:      25,
	MetricAlertness:          30,
	MetricCognitiveLoad:      35,
	MetricMotivation:         35,
	MetricMoodValence:        60,
	MetricStress:             55,
	MetricAnxiety:            55,
	MetricIrritability:       50,
	MetricLoneliness:         50,
	MetricSocialEnergy:       40,
	MetricLibido:             60,
	MetricSexualSatisfaction: 50,
	MetricMissing:            45,
	MetricTrust:              5,
	MetricIntimacy:           6,
}

var conversationTraitCap = map[TraitID]float64{
	TraitResilience:       0.5,
	TraitFitness:          0.5,
	TraitSleepDebt:        20,
	TraitSensitivity:      0.5,
	TraitAttachmentSecure: 1.0,
	TraitNutritionBalance: 0.5,
}

var conversationMetricAliases = map[string]MetricID{
	"生命力": MetricVitality, "精力": MetricVitality, "体力": MetricVitality,
	"饥饿": MetricHunger, "饥饿值": MetricHunger,
	"疲劳": MetricFatigue, "疲劳值": MetricFatigue,
	"睡眠压力": MetricSleepPressure, "睡意": MetricSleepPressure,
	"清醒度": MetricAlertness, "认知负荷": MetricCognitiveLoad, "行动动机": MetricMotivation,
	"情绪": MetricMoodValence, "心情": MetricMoodValence, "情绪值": MetricMoodValence,
	"压力": MetricStress, "焦虑": MetricAnxiety, "易怒": MetricIrritability,
	"孤独": MetricLoneliness, "孤独感": MetricLoneliness,
	"社交电量": MetricSocialEnergy, "性欲": MetricLibido, "性欲值": MetricLibido,
	"性满足": MetricSexualSatisfaction, "性满足度": MetricSexualSatisfaction,
	"思念": MetricMissing, "思念度": MetricMissing,
	"信任": MetricTrust, "信任度": MetricTrust, "亲密": MetricIntimacy, "亲密度": MetricIntimacy,
}

var conversationTraitAliases = map[string]TraitID{
	"韧性": TraitResilience, "体能": TraitFitness, "睡眠债": TraitSleepDebt,
	"敏感度": TraitSensitivity, "关系安全感": TraitAttachmentSecure, "饮食稳定度": TraitNutritionBalance,
}

func canonicalizeConversationOutcome(outcome ConversationOutcome) ConversationOutcome {
	if len(outcome.Impulses) > 0 {
		normalized := make(map[MetricID]float64, len(outcome.Impulses))
		for rawID, delta := range outcome.Impulses {
			id := rawID
			if alias, ok := conversationMetricAliases[string(rawID)]; ok {
				id = alias
			}
			normalized[id] = delta
		}
		outcome.Impulses = normalized
	}
	if len(outcome.TraitChanges) > 0 {
		normalized := make(map[TraitID]float64, len(outcome.TraitChanges))
		for rawID, delta := range outcome.TraitChanges {
			id := rawID
			if alias, ok := conversationTraitAliases[string(rawID)]; ok {
				id = alias
			}
			normalized[id] = delta
		}
		outcome.TraitChanges = normalized
	}
	for i := range outcome.Effects {
		if alias, ok := conversationMetricAliases[string(outcome.Effects[i].Target)]; ok {
			outcome.Effects[i].Target = alias
		}
	}
	return outcome
}
