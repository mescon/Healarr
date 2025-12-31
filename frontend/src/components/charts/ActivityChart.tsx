import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts';
import { useQuery } from '@tanstack/react-query';
import { getStatsHistory } from '../../lib/api';
import { format, parseISO } from 'date-fns';

const ActivityChart = () => {
    const { data: history, isLoading } = useQuery({
        queryKey: ['statsHistory'],
        queryFn: getStatsHistory,
    });

    if (isLoading) return <div className="h-64 flex items-center justify-center text-slate-500">Loading chart...</div>;
    if (!history || history.length === 0) return <div className="h-64 flex items-center justify-center text-slate-500">No activity data available</div>;

    return (
        <div className="h-64 w-full min-h-[250px]">
            <ResponsiveContainer width="100%" height="100%" minWidth={200} minHeight={200}>
                <AreaChart data={history}>
                    <defs>
                        <linearGradient id="colorCount" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="5%" stopColor="#ef4444" stopOpacity={0.3} />
                            <stop offset="95%" stopColor="#ef4444" stopOpacity={0} />
                        </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="#334155" vertical={false} />
                    <XAxis
                        dataKey="date"
                        stroke="#94a3b8"
                        tickFormatter={(str) => format(parseISO(str), 'MMM d')}
                        tick={{ fontSize: 12 }}
                        tickLine={false}
                        axisLine={false}
                    />
                    <YAxis
                        stroke="#94a3b8"
                        tick={{ fontSize: 12 }}
                        tickLine={false}
                        axisLine={false}
                    />
                    <Tooltip
                        contentStyle={{ backgroundColor: '#1e293b', borderColor: '#334155', color: '#f8fafc' }}
                        itemStyle={{ color: '#f8fafc' }}
                        labelFormatter={(str) => format(parseISO(str), 'MMM d, yyyy')}
                    />
                    <Area
                        type="monotone"
                        dataKey="count"
                        stroke="#ef4444"
                        fillOpacity={1}
                        fill="url(#colorCount)"
                    />
                </AreaChart>
            </ResponsiveContainer>
        </div>
    );
};

export default ActivityChart;
