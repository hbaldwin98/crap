namespace Conformance;

public class Callables
{
    public int Score => ready ? 1 : 0;
    public int this[int value] => value > 0 && ready ? value : 0;
    public int Initialized { get; } = ready ? 1 : 0;

    public int Outer(bool flag)
    {
        Func<int, int> choose = value => {
            if (value > 0) return value;
            return 0;
        };
        Func<int, int> callback = delegate(int value) {
            while (value > 0) value--;
            return value;
        };
        if (flag) return choose(1);
        return callback(0);
    }

    public void Register()
    {
        Use(value => value > 0 ? value : 0);
    }
}
