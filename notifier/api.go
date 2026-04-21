package notifier

type Simple interface {
	Send(message string) error
}
