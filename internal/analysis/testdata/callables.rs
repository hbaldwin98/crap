pub struct Worker;

impl Worker {
    pub fn outer(&self, flag: bool) -> i32 {
        let handler = |value: i32| {
            if value > 0 && flag {
                return value;
            }
            0
        };
        if flag {
            return handler(1);
        }
        0
    }
}

mod registry {
    pub fn register(values: &[i32]) -> usize {
        values.iter().filter(|value| **value > 0 || **value < -10).count()
    }
}
