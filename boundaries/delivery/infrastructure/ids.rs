//! Идентификаторы `Delivery` и offer в production-сборке (FR-541): UUIDv7
//! уникален между перезапусками и репликами процесса, поэтому новые id не
//! совпадают с уже сохранёнными в PostgreSQL. Колонки id — `TEXT`, схема не
//! меняется. Последовательный генератор `infrastructure::memory` — только для
//! тестов.

use uuid::Uuid;

use crate::pkg::usecase::IdGenerator;

/// UUIDv7 в каноническом текстовом виде; упорядочен по времени создания.
#[derive(Clone, Copy, Debug, Default)]
pub struct UuidIdGenerator;

impl IdGenerator for UuidIdGenerator {
    fn next_id(&self) -> String {
        Uuid::now_v7().to_string()
    }
}

#[cfg(test)]
mod tests {
    use std::collections::HashSet;

    use super::*;

    /// AC-631: a new generator (a restarted process or another replica) does
    /// not repeat identifiers issued before.
    #[test]
    fn identifiers_are_unique_across_restarts() {
        let before_restart: Vec<String> = (0..100).map(|_| UuidIdGenerator.next_id()).collect();
        let after_restart = UuidIdGenerator;
        let next = after_restart.next_id();

        assert!(!before_restart.contains(&next));
        let unique: HashSet<&String> = before_restart.iter().collect();
        assert_eq!(unique.len(), before_restart.len());
        assert_eq!(Uuid::parse_str(&next).map(|id| id.get_version_num()), Ok(7));
    }
}
