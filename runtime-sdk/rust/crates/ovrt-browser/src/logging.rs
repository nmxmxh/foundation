use log::{Level, LevelFilter, Metadata, Record};

struct RuntimeLogger;

impl log::Log for RuntimeLogger {
    fn enabled(&self, metadata: &Metadata) -> bool {
        metadata.level() <= Level::Info
    }

    fn log(&self, record: &Record) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let level = match record.level() {
            Level::Error => 0,
            Level::Warn => 1,
            Level::Info => 2,
            Level::Debug => 3,
            Level::Trace => 4,
        };
        crate::js_interop::console_log(&format!("[{}] {}", record.target(), record.args()), level);
    }

    fn flush(&self) {}
}

static LOGGER: RuntimeLogger = RuntimeLogger;

pub fn init_logging() {
    let _ = log::set_logger(&LOGGER).map(|_| log::set_max_level(LevelFilter::Info));
    std::panic::set_hook(Box::new(|info| {
        let message = if let Some(message) = info.payload().downcast_ref::<&str>() {
            (*message).to_string()
        } else if let Some(message) = info.payload().downcast_ref::<String>() {
            message.clone()
        } else {
            "unspecified panic".to_string()
        };
        let location = info
            .location()
            .map(|location| {
                format!(
                    " at {}:{}:{}",
                    location.file(),
                    location.line(),
                    location.column()
                )
            })
            .unwrap_or_default();
        crate::js_interop::console_log(
            &format!("[ovrt-browser] panic: {}{}", message, location),
            0,
        );
    }));
}
