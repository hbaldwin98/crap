namespace Conformance {
    export function outer(flag: boolean): number {
        const nested = (value: number): number => flag && value > 0 ? value : 0;
        if (flag) return nested(1);
        return 0;
    }

    export class Worker {
        run(value: number): void {
            try {
                if (value > 0) return;
            } catch {
                return;
            }
        }
    }
}
