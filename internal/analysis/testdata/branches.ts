export function count(value: number, ready: boolean, values: number[]): number {
    if (ready) value++;
    for (let index = 0; index < value; index++) value--;
    for (const item of values) value += item;
    while (ready) break;
    do { value--; } while (ready);
    try { value++; } catch { value--; }
    switch (value) { case 1: break; case 2: break; default: break; }
    value = ready ? 1 : 0;
    ready = ready && value > 0 || value < -10 ?? false;
    return value;
}
