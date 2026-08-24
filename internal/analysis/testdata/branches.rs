pub fn count(value: i32, ready: bool, text: &str) -> Result<i32, String> {
    if ready {
        return Ok(1);
    } else if value > 0 {
        return Ok(2);
    }
    while ready {
        break;
    }
    for _ in 0..value {
        break;
    }
    match value {
        1 | 2 => {}
        3 => {}
        _ => {}
    }
    let parsed: i32 = text.trim().parse().or(Err("bad".to_string()))?;
    if ready && value != 0 || parsed > 0 {
        return Ok(3);
    }
    Ok(0)
}
