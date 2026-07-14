package notifier

// Notifier sends desktop notifications to the user.
type Notifier interface {
	Notify(title, message string, timeoutSec int)
}

// NoopNotifier discards all notifications.
type NoopNotifier struct{}

func (NoopNotifier) Notify(string, string, int) {}
