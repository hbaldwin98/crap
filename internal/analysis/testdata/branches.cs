class Branches
{
    int Count(int value, bool ready)
    {
        if (ready) value++;
        for (var i = 0; i < value; i++) { }
        foreach (var item in items) { }
        while (ready) break;
        do { value--; } while (ready);
        try { value++; } catch { value--; }
        switch (value) { case 1: break; default: break; }
        value = value switch { > 0 => 1, _ => 0 };
        value = ready ? 1 : 0;
        ready = value is > 0 and < 10;
        ready = value is 20 or 30;
        ready = ready && other || third ?? fallback;
        return value;
    }
}
