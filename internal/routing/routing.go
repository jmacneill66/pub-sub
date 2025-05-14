package routing


type PlayingState struct {
    IsPaused bool `json:"isPaused"`
}

const (
    ArmyMovesPrefix = "army_moves"
    WarRecognitionsPrefix = "war_recognitions"
    PauseKey = "pause"
    GameLogSlug = "game_logs"
)

const (
    ExchangePerilDirect = "peril_direct"
    ExchangePerilTopic  = "peril_topic"
    ExchangePerilDead = "peril_dlx"

)
