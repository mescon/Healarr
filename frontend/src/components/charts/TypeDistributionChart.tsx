import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer, Legend } from 'recharts';
import { useQuery } from '@tanstack/react-query';
import { getStatsTypes } from '../../lib/api';
import { formatCorruptionType } from '../../lib/formatters';

const COLORS = ['#ef4444', '#f97316', '#eab308', '#3b82f6', '#8b5cf6', '#ec4899'];

const TypeDistributionChart = () => {
    const { data: types, isLoading } = useQuery({
        queryKey: ['statsTypes'],
        queryFn: getStatsTypes,
    });

    if (isLoading) return <div className="h-64 flex items-center justify-center text-slate-500">Loading chart...</div>;
    if (!types || types.length === 0) return <div className="h-64 flex items-center justify-center text-slate-500">No type data available</div>;

    // Transform data with human-friendly labels
    const formattedData = types.map(item => ({
        type: formatCorruptionType(item.type),
        count: item.count
    }));

    return (
        <div className="h-64 w-full min-h-[250px]">
            <ResponsiveContainer width="100%" height="100%" minWidth={200} minHeight={200}>
                <PieChart>
                    <Pie
                        data={formattedData}
                        cx="50%"
                        cy="50%"
                        innerRadius={60}
                        outerRadius={80}
                        paddingAngle={5}
                        dataKey="count"
                        nameKey="type"
                    >
                        {formattedData.map((_, index) => (
                            <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} stroke="rgba(0,0,0,0)" />
                        ))}
                    </Pie>
                    <Tooltip
                        contentStyle={{ backgroundColor: '#1e293b', borderColor: '#334155', color: '#f8fafc' }}
                        itemStyle={{ color: '#f8fafc' }}
                    />
                    <Legend />
                </PieChart>
            </ResponsiveContainer>
        </div>
    );
};

export default TypeDistributionChart;
